package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ProtocolVersion 是本客户端声明支持的 MCP 协议版本。
// 服务器可以在 initialize 响应里回一个它支持的旧版本；本客户端只用到 tools 能力，各版本在这部分没有差别，都接受。
const ProtocolVersion = "2025-06-18"

// ---- JSON-RPC 2.0 消息 ----

// message 是一条 JSON-RPC 消息。请求、响应、通知共用一个结构，按字段是否存在区分：
// 有 method 有 id 是请求，有 method 无 id 是通知，无 method 有 id 是响应。
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (m *message) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }
func (m *message) isRequest() bool  { return m.Method != "" && len(m.ID) > 0 }

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

// JSON-RPC 规定的错误码：服务器向我们发了不支持的请求时使用。
const codeMethodNotFound = -32601

func newRequest(id int64, method string, params any) (message, error) {
	m := message{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return m, err
		}
		m.Params = b
	}
	return m, nil
}

// replyTo 生成对服务器请求的回复。我们只支持 ping（规范要求客户端必须响应），其余一律"方法不存在"。
func replyTo(req message) message {
	if req.Method == "ping" {
		return message{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage("{}")}
	}
	return message{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: codeMethodNotFound, Message: "method not supported by client: " + req.Method}}
}

// transport 是一种传输方式（stdio 或 HTTP）。它只负责"发一个请求，等对应的响应"，不理解 MCP 语义。
type transport interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(ctx context.Context, method string, params any) error
	// done 在连接断开（例如子进程退出）时关闭；HTTP 是无连接的，返回 nil。
	done() <-chan struct{}
	// err 是连接断开的原因（done 关闭后有效）。
	err() error
	close() error
}

// ---- MCP 客户端 ----

// RPCClient 是 Client 的真实实现：在某种传输之上完成握手，提供 tools/list 和 tools/call。
type RPCClient struct {
	t transport
	// ServerName / ServerVersion 来自 initialize 响应，界面上显示。
	ServerName    string
	ServerVersion string
	// Instructions 是服务器给模型的使用说明（可选）。目前只在界面上展示，没有拼进系统提示词。
	Instructions string
}

// Connect 按配置建立连接并完成 initialize 握手。ctx 只约束握手阶段，不约束连接的生命周期。
func Connect(ctx context.Context, cfg ServerConfig) (*RPCClient, error) {
	var (
		t   transport
		err error
	)
	switch cfg.Transport {
	case "", TransportStdio:
		t, err = startStdio(cfg)
	case TransportHTTP:
		t, err = newHTTP(cfg)
	default:
		return nil, fmt.Errorf("mcp: unknown transport %q", cfg.Transport)
	}
	if err != nil {
		return nil, err
	}
	c := &RPCClient{t: t}
	if err := c.initialize(ctx); err != nil {
		t.close()
		return nil, err
	}
	return c, nil
}

// initialize 是 MCP 的握手：声明协议版本和客户端信息，然后发 notifications/initialized 通知。
// 我们不提供任何客户端能力（roots、sampling、elicitation），capabilities 留空对象。
func (c *RPCClient) initialize(ctx context.Context) error {
	raw, err := c.t.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "myllmapp", "version": "0.1"},
	})
	if err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools *json.RawMessage `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp: parse initialize result: %w", err)
	}
	if res.ProtocolVersion == "" {
		return errors.New("mcp: initialize result has no protocolVersion")
	}
	if h, ok := c.t.(*httpTransport); ok {
		h.setProtocolVersion(res.ProtocolVersion)
	}
	c.ServerName, c.ServerVersion, c.Instructions = res.ServerInfo.Name, res.ServerInfo.Version, res.Instructions
	return c.t.notify(ctx, "notifications/initialized", nil)
}

// maxListPages 防止一个出错的服务器无限返回 nextCursor。
const maxListPages = 100

// ListTools 列出全部工具（自动翻页）。
func (c *RPCClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var out []ToolInfo
	cursor := ""
	for range maxListPages {
		var params any
		if cursor != "" {
			params = map[string]string{"cursor": cursor}
		}
		raw, err := c.t.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("mcp: parse tools/list: %w", err)
		}
		for _, t := range res.Tools {
			out = append(out, ToolInfo{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
	return out, nil
}

// CallTool 调用一个工具。结果里的文本内容按顺序拼接；非文本内容（图片、音频、资源）用一行占位说明，
// 因为目前的工具结果只支持文本（见 tool.Result）。
func (c *RPCClient) CallTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	raw, err := c.t.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return CallResult{}, err
	}
	var res struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Resource *struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"resource"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, fmt.Errorf("mcp: parse tools/call: %w", err)
	}
	var parts []string
	for _, b := range res.Content {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "resource":
			if b.Resource != nil && b.Resource.Text != "" {
				parts = append(parts, b.Resource.Text)
			} else if b.Resource != nil {
				parts = append(parts, "[resource "+b.Resource.URI+"]")
			}
		default:
			parts = append(parts, fmt.Sprintf("[%s content omitted (%s)]", b.Type, b.MimeType))
		}
	}
	// 只返回结构化结果的服务器：把 JSON 原样交给模型。
	if len(parts) == 0 && len(res.StructuredContent) > 0 {
		parts = append(parts, string(res.StructuredContent))
	}
	return CallResult{Text: strings.Join(parts, "\n"), IsError: res.IsError}, nil
}

// Done 在连接断开时关闭（stdio 子进程退出）；HTTP 传输返回 nil（永不关闭）。
func (c *RPCClient) Done() <-chan struct{} { return c.t.done() }

// Err 返回连接断开的原因（例如子进程的退出码和 stderr 末尾）。连接正常时为 nil。
func (c *RPCClient) Err() error { return c.t.err() }

func (c *RPCClient) Close() error { return c.t.close() }
