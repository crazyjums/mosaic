package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider/sse"
)

// httpTransport 实现 MCP 的 Streamable HTTP 传输（2025-03-26 起的规范）：
//
//   - 每条消息是一次 POST；服务器对请求的回复要么是一个 JSON 响应，要么是一个 SSE 流（流里可能先有通知，最后是响应）；
//   - 服务器在 initialize 响应头里给出 Mcp-Session-Id，之后每个请求都要带上；
//   - 关闭时发 DELETE 结束会话。
//
// 没有实现"GET 打开服务器推送流"：它只用于服务器主动通知（例如工具列表变化），我们目前不处理通知。
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	nextID atomic.Int64

	mu              sync.Mutex
	sessionID       string
	protocolVersion string
}

func newHTTP(cfg ServerConfig) (*httpTransport, error) {
	if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
		return nil, fmt.Errorf("mcp: url must start with http:// or https:// (got %q)", cfg.URL)
	}
	// 不设整体超时：工具调用可能很久，由调用方的 ctx 控制。
	return &httpTransport{url: cfg.URL, headers: cfg.Headers, client: &http.Client{}}, nil
}

func (t *httpTransport) setProtocolVersion(v string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.protocolVersion = v
}

func (t *httpTransport) newPost(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.setCommonHeaders(req)
	return req, nil
}

func (t *httpTransport) setCommonHeaders(req *http.Request) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	if t.protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", t.protocolVersion)
	}
}

func (t *httpTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	reqMsg, err := newRequest(t.nextID.Add(1), method, params)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(reqMsg)
	req, err := t.newPost(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}

	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var m message
	switch ct {
	case "text/event-stream":
		m, err = t.readStream(ctx, resp.Body, string(reqMsg.ID))
	default: // application/json；有的服务器不写 Content-Type，也按 JSON 处理
		err = json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&m)
	}
	if err != nil {
		return nil, fmt.Errorf("mcp: read response of %s: %w", method, err)
	}
	if m.Error != nil {
		return nil, m.Error
	}
	return m.Result, nil
}

// readStream 从 SSE 流里找到与请求 id 对应的响应。流里的服务器请求（如 ping）通过新的 POST 回复。
func (t *httpTransport) readStream(ctx context.Context, body io.Reader, id string) (message, error) {
	r := sse.NewReader(body)
	for {
		ev, err := r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return message{}, errors.New("stream ended without a response")
			}
			return message{}, err
		}
		var m message
		if json.Unmarshal([]byte(ev.Data), &m) != nil {
			continue
		}
		switch {
		case m.isResponse() && string(m.ID) == id:
			return m, nil
		case m.isRequest():
			go t.send(ctx, replyTo(m))
		}
	}
}

// send 发一条不需要响应的消息（通知，或对服务器请求的回复）。规范要求服务器回 202 Accepted。
func (t *httpTransport) send(ctx context.Context, m message) error {
	body, _ := json.Marshal(m)
	req, err := t.newPost(ctx, body)
	if err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return checkStatus(resp)
}

func (t *httpTransport) notify(ctx context.Context, method string, params any) error {
	m := message{JSONRPC: "2.0", Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = b
	}
	return t.send(ctx, m)
}

func (t *httpTransport) done() <-chan struct{} { return nil }
func (t *httpTransport) err() error            { return nil }

// close 用 DELETE 结束服务器端的会话（尽力而为：规范允许服务器回 405 表示不支持）。
func (t *httpTransport) close() error {
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil)
	if err != nil {
		return nil
	}
	t.setCommonHeaders(req)
	if resp, err := t.client.Do(req); err == nil {
		resp.Body.Close()
	}
	return nil
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := strings.TrimSpace(string(b))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("mcp: http %d（认证失败，请检查请求头里的令牌）: %s", resp.StatusCode, msg)
	case http.StatusNotFound:
		return fmt.Errorf("mcp: http 404（地址不对，或服务器端会话已过期）: %s", msg)
	}
	return fmt.Errorf("mcp: http %d: %s", resp.StatusCode, msg)
}
