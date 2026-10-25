package siwc

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AppName 是登录时向用户显示的应用名（agent_name_hint）。条款要求使用应用自己的真实名字，并在各安装之间保持一致。
const AppName = "myllmapp"

// loginTimeout 是等待用户在浏览器里完成登录的时间。
const loginTimeout = 10 * time.Minute

// ErrNotLoggedIn 表示还没有登录（或登录已失效）。
var ErrNotLoggedIn = errors.New("尚未登录 ChatGPT 账号（设置 → 模型厂商 → ChatGPT 账号）")

// State 是登录状态。
type State string

const (
	StateLoggedOut State = "logged_out"
	StatePending   State = "pending" // 已打开浏览器，等待用户完成登录
	StateLoggedIn  State = "logged_in"
)

// Status 是给界面看的登录状态。不含任何令牌。
type Status struct {
	State State  `json:"state"`
	Email string `json:"email,omitempty"`
	Plan  string `json:"plan,omitempty"`
	// LoginURL 是等待登录时的授权地址：浏览器没有自动打开时，用户可以复制它。
	LoginURL string `json:"login_url,omitempty"`
	// Error 是最近一次登录 / 刷新失败的原因。
	Error string `json:"error,omitempty"`
	// Registered 为 true 表示本机已经有签发的 client_id，下次登录不需要重新注册。
	Registered bool `json:"registered"`
}

// Options 用于测试时替换地址、HTTP 客户端和时钟。
type Options struct {
	Endpoints  Endpoints
	HTTPClient *http.Client
	Now        func() time.Time
}

// Manager 管理登录状态，并作为 provider.TokenSource 提供访问令牌。所有方法并发安全。
type Manager struct {
	store store
	ep    Endpoints
	http  *http.Client
	now   func() time.Time

	mu       sync.Mutex
	cred     *Credential
	pending  *pendingLogin
	lastErr  string
	onChange func()

	refreshMu sync.Mutex // 串行化刷新（刷新令牌会轮换，并发刷新会互相作废）
	disc      *discovery
}

type pendingLogin struct {
	url    string
	cancel context.CancelFunc
}

// NewManager 读取 dir 下保存的登录信息。
func NewManager(dir string, opts Options) *Manager {
	if opts.Endpoints.Issuer == "" {
		opts.Endpoints = DefaultEndpoints
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	m := &Manager{store: store{dir: dir}, ep: opts.Endpoints, http: noRedirectClient(opts.HTTPClient), now: opts.Now}
	if c, err := m.store.credential(); err == nil {
		m.cred = c
	} else {
		m.lastErr = "读取登录信息失败：" + err.Error()
	}
	return m
}

// OnChange 注册登录状态变化（登录成功、退出、令牌失效）时的回调。回调在不持有锁的情况下调用。
func (m *Manager) OnChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = fn
}

func (m *Manager) changed() {
	m.mu.Lock()
	fn := m.onChange
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// LoggedIn 报告当前是否有可用的登录。
func (m *Manager) LoggedIn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cred != nil
}

// Status 返回登录状态。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{State: StateLoggedOut, Error: m.lastErr, Registered: m.store.registration().ClientID != ""}
	if m.cred != nil {
		st.State, st.Email, st.Plan = StateLoggedIn, m.cred.Email, m.cred.Plan
	}
	if m.pending != nil {
		st.State, st.LoginURL = StatePending, m.pending.url
	}
	return st
}

// ---- 登录 ----

// StartLogin 开始登录：在本机开一个回调服务器，返回需要在浏览器里打开的授权地址。
// 登录在后台完成（或超时 / 被取消），完成后状态变化会通过 OnChange 通知。
//
// newAccount 为 true 时重新走动态注册（换一个 ChatGPT 账号）；否则复用本机已签发的 client_id。
func (m *Manager) StartLogin(newAccount bool) (string, error) {
	m.CancelLogin()

	hostID, err := m.store.hostID()
	if err != nil {
		return "", err
	}
	reg := m.store.registration()
	clientID := reg.ClientID
	if newAccount || clientID == "" {
		clientID = DynamicClient
	}

	// 回调地址必须是 127.0.0.1（文档明确要求不能用 localhost），端口随机。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("siwc: open callback listener: %w", err)
	}
	host := ln.Addr().String()
	redirectURI := "http://" + host + "/auth/callback"
	verifier, state, nonce := randomString(32), randomString(24), randomString(24)

	q := url.Values{}
	q.Set("client_id", clientID)
	if clientID == DynamicClient {
		q.Set("agent_name_hint", AppName)
	} else if reg.Email != "" {
		q.Set("login_hint", reg.Email)
	}
	q.Set("ext_agent_host_id", hostID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", Scope)
	q.Set("resource", m.ep.Resource)
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", pkceChallenge(verifier))
	authURL := m.ep.AuthorizeURL + "?" + q.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	p := &pendingLogin{url: authURL, cancel: cancel}
	m.mu.Lock()
	m.pending, m.lastErr = p, ""
	m.mu.Unlock()

	flow := &loginFlow{m: m, host: host, redirectURI: redirectURI, clientID: clientID, hostID: hostID,
		verifier: verifier, state: state, nonce: nonce, done: make(chan struct{})}
	go flow.run(ctx, ln, p)
	return authURL, nil
}

// CancelLogin 取消正在进行的登录（如果有）。
func (m *Manager) CancelLogin() {
	m.mu.Lock()
	p := m.pending
	m.pending = nil
	m.mu.Unlock()
	if p != nil {
		p.cancel()
	}
}

// loginFlow 是一次登录：回调服务器只接受一次有效请求。
type loginFlow struct {
	m                                   *Manager
	host, redirectURI, clientID, hostID string
	verifier, state, nonce              string

	once sync.Once
	done chan struct{}
	err  error
}

func (f *loginFlow) run(ctx context.Context, ln net.Listener, p *pendingLogin) {
	srv := &http.Server{Handler: http.HandlerFunc(f.serve), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	var err error
	select {
	case <-f.done:
		err = f.err
	case <-ctx.Done():
		err = errors.New("登录超时或已取消")
		if errors.Is(ctx.Err(), context.Canceled) {
			err = nil // 用户主动取消，不算错误
		}
	}
	// 给浏览器一点时间收到"登录成功"页面，再关闭服务器。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	p.cancel()

	m := f.m
	m.mu.Lock()
	if m.pending == p {
		m.pending = nil
		if err != nil {
			m.lastErr = err.Error()
		}
	}
	m.mu.Unlock()
	m.changed()
}

// serve 处理回调。检查顺序：路径与 Host → state（常量时间比较）→ error → code。
func (f *loginFlow) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/auth/callback" || r.Host != f.host {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if !eq(q.Get("state"), f.state) {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	finished := false
	f.once.Do(func() {
		finished = true
		f.err = f.complete(r.Context(), q)
		close(f.done)
	})
	if !finished { // 只接受一次
		http.NotFound(w, r)
		return
	}
	writeResultPage(w, f.err)
}

func (f *loginFlow) complete(ctx context.Context, q url.Values) error {
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			return errors.New("你在浏览器中拒绝了授权")
		}
		return fmt.Errorf("授权失败：%s %s", e, q.Get("error_description"))
	}
	code := q.Get("code")
	if code == "" {
		return errors.New("回调中没有授权码")
	}
	// 首次注册时，回调带回 OpenAI 签发给本应用的 client_id；之后用它换令牌。
	clientID := f.clientID
	if issued := q.Get("client_id"); issued != "" {
		clientID = issued
	}
	if clientID == DynamicClient {
		return errors.New("回调中没有签发的 client_id，无法完成注册")
	}

	m := f.m
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientID)
	form.Set("code", code)
	form.Set("code_verifier", f.verifier)
	form.Set("redirect_uri", f.redirectURI)
	form.Set("resource", m.ep.Resource)
	var tr tokenResponse
	if err := postForm(ctx, m.http, m.ep.TokenURL, form, &tr); err != nil {
		return fmt.Errorf("换取令牌失败：%w", err)
	}
	cred, err := m.credentialFrom(ctx, tr, nil, clientID, f.hostID, f.nonce)
	if err != nil {
		return err
	}
	if err := m.store.saveRegistration(Registration{ClientID: clientID, Subject: cred.Subject, Email: cred.Email}); err != nil {
		return err
	}
	if err := m.store.saveCredential(cred); err != nil {
		return err
	}
	m.mu.Lock()
	m.cred, m.lastErr = cred, ""
	m.mu.Unlock()
	return nil
}

// credentialFrom 校验令牌响应并生成要保存的凭据。prev 非空表示这是一次刷新（沿用上次的账号信息）。
func (m *Manager) credentialFrom(ctx context.Context, tr tokenResponse, prev *Credential, clientID, hostID, nonce string) (*Credential, error) {
	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, errors.New("令牌响应缺少 access_token 或 refresh_token")
	}
	if tr.Scope != "" && !strings.Contains(" "+tr.Scope+" ", " "+usePlanScope+" ") {
		return nil, errors.New("授权中没有包含使用 ChatGPT 套餐额度的权限，请重新登录并同意授权")
	}
	now := m.now()
	c := &Credential{
		Issuer: m.ep.Issuer, ClientID: clientID, HostID: hostID,
		AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken, IDToken: tr.IDToken,
		TokenType: tr.TokenType, Scopes: tr.Scope,
		ExpiresAt:         now.Add(time.Duration(max(tr.ExpiresIn, 60)) * time.Second),
		EarliestRefreshAt: parseTime(tr.EarliestRefreshAt),
		SavedAt:           now,
	}
	if prev != nil {
		c.Subject, c.Email, c.Plan = prev.Subject, prev.Email, prev.Plan
		if c.IDToken == "" {
			c.IDToken = prev.IDToken
		}
		if c.Scopes == "" {
			c.Scopes = prev.Scopes
		}
	}
	// 登录时必须有 ID 令牌并通过校验；刷新时如果返回了新的 ID 令牌也校验（没有 nonce）。
	if tr.IDToken != "" || prev == nil {
		if tr.IDToken == "" {
			return nil, errors.New("令牌响应缺少 id_token")
		}
		d, err := m.discovery(ctx)
		if err != nil {
			return nil, err
		}
		claims, err := verifyIDToken(ctx, m.http, d, tr.IDToken, clientID, nonce, now)
		if err != nil {
			return nil, err
		}
		if prev != nil && claims.Sub != prev.Subject {
			return nil, errors.New("刷新后的账号与登录的账号不一致")
		}
		c.Subject, c.Email, c.Plan = claims.Sub, claims.email(), claims.plan()
	}
	return c, nil
}

func (m *Manager) discovery(ctx context.Context) (*discovery, error) {
	m.mu.Lock()
	d := m.disc
	m.mu.Unlock()
	if d != nil {
		return d, nil
	}
	d, err := fetchDiscovery(ctx, m.http, m.ep)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.disc = d
	m.mu.Unlock()
	return d, nil
}

// ---- 令牌 ----

// refreshMargin：访问令牌剩余有效期少于这个值时就刷新，避免请求发出去的途中过期。
const refreshMargin = 2 * time.Minute

// Token 返回可用的访问令牌，必要时先刷新。实现 provider.TokenSource。
func (m *Manager) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	c := m.cred
	m.mu.Unlock()
	if c == nil {
		return "", ErrNotLoggedIn
	}
	if !m.needsRefresh(c) {
		return c.AccessToken, nil
	}

	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	var token string
	err := m.store.withLock(func() error {
		// 锁内重新读文件：可能另一个进程（或本进程的另一个请求）刚刚刷新过。
		fresh, err := m.store.credential()
		if err != nil {
			return err
		}
		if fresh == nil {
			return ErrNotLoggedIn
		}
		if !m.needsRefresh(fresh) {
			m.setCred(fresh)
			token = fresh.AccessToken
			return nil
		}
		next, err := m.refresh(ctx, fresh)
		if err != nil {
			return err
		}
		if err := m.store.saveCredential(next); err != nil {
			return err
		}
		m.setCred(next)
		token = next.AccessToken
		return nil
	})
	if err == nil {
		return token, nil
	}
	if isInvalidGrant(err) || errors.Is(err, ErrNotLoggedIn) {
		// 刷新令牌已失效（过期 30 天、被吊销、或在别处被使用过）：只能重新登录。
		_ = m.store.deleteCredential()
		m.mu.Lock()
		m.cred = nil
		m.lastErr = "ChatGPT 登录已过期，请重新登录"
		m.mu.Unlock()
		m.changed()
		return "", errors.New("ChatGPT 登录已过期，请在 设置 → 模型厂商 中重新登录")
	}
	return "", fmt.Errorf("刷新 ChatGPT 令牌失败：%w", err)
}

func (m *Manager) needsRefresh(c *Credential) bool {
	now := m.now()
	if !now.Before(c.ExpiresAt) {
		return true // 已经过期：不管 earliest_refresh_at，必须刷新
	}
	if now.Before(c.EarliestRefreshAt) {
		return false
	}
	return c.ExpiresAt.Sub(now) < refreshMargin
}

func (m *Manager) setCred(c *Credential) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cred = c
}

// refresh 用刷新令牌换新令牌。不带 scope（保持原有授权）；成功后旧的刷新令牌立即作废。
func (m *Manager) refresh(ctx context.Context, c *Credential) (*Credential, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.ClientID)
	form.Set("refresh_token", c.RefreshToken)
	form.Set("resource", m.ep.Resource)
	var tr tokenResponse
	if err := postForm(ctx, m.http, m.ep.TokenURL, form, &tr); err != nil {
		return nil, err
	}
	return m.credentialFrom(ctx, tr, c, c.ClientID, c.HostID, "")
}

// ---- 退出 ----

// Logout 吊销刷新令牌并删除本地令牌。吊销失败（例如断网）也会删除本地令牌，但返回错误让界面提示。
// 签发的 client_id 保留，下次登录不用重新注册。
func (m *Manager) Logout(ctx context.Context) error {
	m.CancelLogin()
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	var revokeErr error
	_ = m.store.withLock(func() error {
		c, _ := m.store.credential()
		if c != nil {
			revokeErr = m.revoke(ctx, c)
		}
		return m.store.deleteCredential()
	})
	m.mu.Lock()
	m.cred, m.lastErr = nil, ""
	m.mu.Unlock()
	m.changed()
	if revokeErr != nil {
		return fmt.Errorf("已在本机退出登录，但吊销令牌失败（可以在 ChatGPT 的设置中手动移除授权）：%w", revokeErr)
	}
	return nil
}

func (m *Manager) revoke(ctx context.Context, c *Credential) error {
	d, err := m.discovery(ctx)
	if err != nil {
		return err
	}
	if d.RevocationEndpoint == "" {
		return errors.New("授权服务器没有提供吊销地址")
	}
	form := url.Values{}
	form.Set("token", c.RefreshToken)
	form.Set("token_type_hint", "refresh_token")
	form.Set("client_id", c.ClientID)
	// 网络错误和 5xx 重试，最多三次；4xx 不重试。空的 200 表示成功（对已经失效的令牌也是）。
	for attempt := 0; ; attempt++ {
		err = postForm(ctx, m.http, d.RevocationEndpoint, form, nil)
		var oe *oauthError
		if err == nil || attempt == 2 || (errors.As(err, &oe) && oe.Status < 500) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 300 * time.Millisecond)
	}
}

// writeResultPage 是浏览器在回调后看到的页面。
func writeResultPage(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	title, msg := "登录成功 · Signed in", "可以关闭这个页面，回到 myllmapp 了。<br>You can close this tab and return to myllmapp."
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		title, msg = "登录失败 · Sign-in failed", html.EscapeString(err.Error())
	}
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1rem;text-align:center">
<h2>%s</h2><p style="color:#666">%s</p></body>`, title, title, msg)
}
