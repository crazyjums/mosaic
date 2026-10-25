package codexquota

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultUsageURL 是 ChatGPT / Codex 自己使用的用量接口（未公开）。
const DefaultUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// cacheTTL：状态栏会频繁刷新，接口本身也是延迟更新的，一分钟查一次足够。
const cacheTTL = time.Minute

// Window 是一个限额窗口。
type Window struct {
	// Seconds 是窗口长度（5 小时 = 18000，每周 = 604800）。
	Seconds     int     `json:"seconds"`
	UsedPercent float64 `json:"used_percent"`
	// ResetAt 是窗口重置的时间；接口没给时为零值。
	ResetAt time.Time `json:"reset_at,omitzero"`
}

// Quota 是剩余额度。
type Quota struct {
	Plan string `json:"plan,omitempty"` // plus / pro / team ……
	// Windows 按窗口长度从短到长排序（通常是 5 小时、每周；有的套餐只有每周）。
	Windows []Window `json:"windows"`
	// Credits 是额外购买的额度余额（接口没给时为 nil）。
	Credits   *float64  `json:"credits,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
}

// ErrNoCodexLogin 表示本机没有 Codex CLI 的登录信息。
var ErrNoCodexLogin = errors.New("没有找到 Codex 的登录信息：请先安装官方 Codex CLI 并运行 codex login（用 ChatGPT 账号登录）")

// Client 查询剩余额度，带一分钟缓存。并发安全。
type Client struct {
	// AuthPath 为空时使用 CODEX_HOME/auth.json 或 ~/.codex/auth.json。
	AuthPath string
	URL      string
	HTTP     *http.Client
	Now      func() time.Time

	mu      sync.Mutex
	cached  *Quota
	lastErr error
	at      time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get 返回剩余额度。一分钟内重复调用返回缓存（包括缓存的错误，避免出错时反复请求）。
func (c *Client) Get(ctx context.Context) (*Quota, error) {
	c.mu.Lock()
	if !c.at.IsZero() && c.now().Sub(c.at) < cacheTTL {
		q, err := c.cached, c.lastErr
		c.mu.Unlock()
		return q, err
	}
	c.mu.Unlock()

	q, err := c.fetch(ctx)
	c.mu.Lock()
	c.cached, c.lastErr, c.at = q, err, c.now()
	c.mu.Unlock()
	return q, err
}

// Invalidate 清除缓存（例如一轮对话结束后想看最新的额度）。
func (c *Client) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = time.Time{}
}

// authPath 返回 Codex CLI 保存登录信息的文件。
func (c *Client) authPath() (string, error) {
	if c.AuthPath != "" {
		return c.AuthPath, nil
	}
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "auth.json"), nil
}

// codexAuth 是 auth.json 里用到的字段。
type codexAuth struct {
	Tokens *struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func (c *Client) credentials() (token, account string, err error) {
	path, err := c.authPath()
	if err != nil {
		return "", "", err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", ErrNoCodexLogin
	}
	if err != nil {
		return "", "", fmt.Errorf("读取 Codex 登录信息失败：%w", err)
	}
	var a codexAuth
	if err := json.Unmarshal(b, &a); err != nil {
		return "", "", fmt.Errorf("Codex 登录信息格式无法识别：%w", err)
	}
	if a.Tokens == nil || a.Tokens.AccessToken == "" {
		// 用 API Key 登录的 Codex 没有套餐额度。
		return "", "", errors.New("Codex 不是用 ChatGPT 账号登录的（或登录信息保存在系统钥匙串里），无法读取套餐额度")
	}
	account = a.Tokens.AccountID
	if account == "" {
		account = accountFromIDToken(a.Tokens.IDToken)
	}
	return a.Tokens.AccessToken, account, nil
}

// accountFromIDToken 从 ID 令牌的声明里取 chatgpt_account_id（只解码、不校验：它只用作请求头）。
func accountFromIDToken(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	_ = json.Unmarshal(b, &claims)
	return claims.Auth.AccountID
}

// usageResponse 是接口响应里用到的字段。未公开的接口，字段都按可选处理。
type usageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit *struct {
		Primary   *rawWindow `json:"primary_window"`
		Secondary *rawWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	Credits *struct {
		Balance json.RawMessage `json:"balance"` // 有时是字符串，有时是数字
	} `json:"credits"`
}

type rawWindow struct {
	UsedPercent       float64 `json:"used_percent"`
	LimitWindowSecond int     `json:"limit_window_seconds"`
	ResetAfterSeconds int     `json:"reset_after_seconds"`
	ResetAt           int64   `json:"reset_at"` // Unix 秒
}

func (c *Client) fetch(ctx context.Context) (*Quota, error) {
	token, account, err := c.credentials()
	if err != nil {
		return nil, err
	}
	url := c.URL
	if url == "" {
		url = DefaultUsageURL
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if account != "" {
		req.Header.Set("ChatGPT-Account-Id", account)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询额度失败：%w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, errors.New("Codex 的登录已过期：运行一次 codex（或 codex login）刷新后再试")
	case resp.StatusCode != http.StatusOK:
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("查询额度失败：HTTP %d %s", resp.StatusCode, msg)
	}
	var r usageResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("额度接口的返回无法识别：%w", err)
	}
	return parse(r, c.now()), nil
}

func parse(r usageResponse, now time.Time) *Quota {
	q := &Quota{Plan: r.PlanType, FetchedAt: now, Windows: []Window{}}
	if r.RateLimit != nil {
		for _, w := range []*rawWindow{r.RateLimit.Primary, r.RateLimit.Secondary} {
			if w == nil {
				continue
			}
			win := Window{Seconds: w.LimitWindowSecond, UsedPercent: clamp(w.UsedPercent)}
			switch {
			case w.ResetAt > 0:
				win.ResetAt = time.Unix(w.ResetAt, 0)
			case w.ResetAfterSeconds > 0:
				win.ResetAt = now.Add(time.Duration(w.ResetAfterSeconds) * time.Second)
			}
			q.Windows = append(q.Windows, win)
		}
	}
	// 窗口在 primary / secondary 中的位置不能说明它的长度（只有每周窗口的套餐会把它放在 primary），按长度排序。
	sort.SliceStable(q.Windows, func(i, j int) bool { return q.Windows[i].Seconds < q.Windows[j].Seconds })
	if r.Credits != nil && len(r.Credits.Balance) > 0 {
		var f float64
		s := strings.Trim(string(r.Credits.Balance), `"`)
		if _, err := fmt.Sscanf(s, "%g", &f); err == nil {
			q.Credits = &f
		}
	}
	return q
}

func clamp(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
