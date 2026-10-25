package siwc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuth 是一个假的授权服务器：发现文档、JWKS、授权（直接重定向回回调）、令牌、吊销。
type fakeAuth struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu        sync.Mutex
	issued    string            // 签发的 client_id
	codes     map[string]string // code → nonce
	verifiers map[string]string // code → code_challenge
	refresh   map[string]bool   // 有效的刷新令牌
	revoked   []string
	n         int
	lastAuth  url.Values
	deny      bool
	noIssued  bool
}

func newFakeAuth(t *testing.T) *fakeAuth {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAuth{t: t, key: key, issued: "oaiapp_test123", codes: map[string]string{}, verifiers: map[string]string{}, refresh: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAuth) endpoints() Endpoints {
	u := f.srv.URL
	return Endpoints{Issuer: u, AuthorizeURL: u + "/api/accounts/authorize", TokenURL: u + "/api/accounts/oauth/token",
		DiscoveryURL: u + "/.well-known/openid-configuration", Resource: "https://api.openai.com/v1"}
}

func (f *fakeAuth) idToken(nonce string) string {
	b64 := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	h := b64(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	now := time.Now().Unix()
	p := b64(map[string]any{"iss": f.srv.URL, "sub": "user-1", "aud": []string{f.issued}, "exp": now + 3600, "iat": now,
		"nonce": nonce, "email": "me@example.com", "https://api.openai.com/auth": map[string]string{"chatgpt_plan_type": "plus"}})
	digest := sha256.Sum256([]byte(h + "." + p))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeAuth) tokens(nonce string, withID bool) map[string]any {
	f.n++
	rt := fmt.Sprintf("rt-%d", f.n)
	f.refresh[rt] = true
	out := map[string]any{"access_token": fmt.Sprintf("at-%d", f.n), "refresh_token": rt, "token_type": "Bearer",
		"expires_in": 3600, "scope": Scope, "earliest_refresh_at": time.Now().Unix()}
	if withID {
		out["id_token"] = f.idToken(nonce)
	}
	return out
}

func (f *fakeAuth) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSON := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(200, map[string]string{"issuer": f.srv.URL, "jwks_uri": f.srv.URL + "/jwks", "revocation_endpoint": f.srv.URL + "/revoke"})
	case "/jwks":
		pub := f.key.PublicKey
		writeJSON(200, map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())}}})
	case "/api/accounts/authorize":
		q := r.URL.Query()
		f.lastAuth = q
		cb, _ := url.Parse(q.Get("redirect_uri"))
		back := url.Values{"state": {q.Get("state")}}
		if f.deny {
			back.Set("error", "access_denied")
		} else {
			code := fmt.Sprintf("code-%d", len(f.codes))
			f.codes[code] = q.Get("nonce")
			f.verifiers[code] = q.Get("code_challenge")
			back.Set("code", code)
			if q.Get("client_id") == DynamicClient && !f.noIssued {
				back.Set("client_id", f.issued)
			}
		}
		cb.RawQuery = back.Encode()
		http.Redirect(w, r, cb.String(), http.StatusFound)
	case "/api/accounts/oauth/token":
		_ = r.ParseForm()
		if r.Form.Get("client_id") != f.issued || r.Form.Get("resource") == "" {
			writeJSON(400, map[string]string{"error": "invalid_client"})
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			code := r.Form.Get("code")
			nonce, ok := f.codes[code]
			if !ok || pkceChallenge(r.Form.Get("code_verifier")) != f.verifiers[code] {
				writeJSON(400, map[string]string{"error": "invalid_grant"})
				return
			}
			delete(f.codes, code)
			writeJSON(200, f.tokens(nonce, true))
		case "refresh_token":
			rt := r.Form.Get("refresh_token")
			if !f.refresh[rt] || r.Form.Get("scope") != "" {
				writeJSON(400, map[string]string{"error": "invalid_grant", "error_description": "refresh token reused"})
				return
			}
			delete(f.refresh, rt) // 轮换：旧的刷新令牌作废
			writeJSON(200, f.tokens("", false))
		}
	case "/revoke":
		_ = r.ParseForm()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		delete(f.refresh, r.Form.Get("token"))
		w.WriteHeader(200)
	default:
		http.NotFound(w, r)
	}
}

// browse 模拟用户在浏览器里打开授权地址：跟随重定向回到本地回调。
func browse(t *testing.T, authURL string) (int, string) {
	t.Helper()
	resp, err := http.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func waitState(t *testing.T, m *Manager, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := m.Status()
		if st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待状态 %s 超时，当前 %+v", want, st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLoginRefreshLogout(t *testing.T) {
	f := newFakeAuth(t)
	dir := t.TempDir()
	var clock = time.Now()
	var clockMu sync.Mutex
	now := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	m := NewManager(dir, Options{Endpoints: f.endpoints(), Now: now})
	changes := make(chan struct{}, 10)
	m.OnChange(func() { changes <- struct{}{} })

	if _, err := m.Token(context.Background()); err != ErrNotLoggedIn {
		t.Fatalf("未登录: %v", err)
	}

	// 首次登录：动态注册。
	authURL, err := m.StartLogin(false)
	if err != nil {
		t.Fatal(err)
	}
	q := f.lastAuthAfter(t, authURL)
	if q.Get("client_id") != DynamicClient || q.Get("agent_name_hint") != AppName || !strings.HasPrefix(q.Get("ext_agent_host_id"), "urn:uuid:") ||
		!strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") || q.Get("code_challenge_method") != "S256" || q.Get("scope") != Scope {
		t.Fatalf("授权参数: %v", q)
	}
	if m.Status().State != StatePending || m.Status().LoginURL != authURL {
		t.Fatal("应处于等待登录状态")
	}
	code, page := browse(t, authURL)
	if code != 200 || !strings.Contains(page, "登录成功") {
		t.Fatalf("回调页面: %d %s", code, page)
	}
	st := waitState(t, m, StateLoggedIn)
	if st.Email != "me@example.com" || st.Plan != "plus" || !st.Registered {
		t.Fatalf("状态: %+v", st)
	}
	<-changes

	// 文件：权限 0600，签发的 client_id 已保存。
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "credential.json"))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("凭据文件权限 %v", fi.Mode().Perm())
		}
	}
	if m.store.registration().ClientID != f.issued {
		t.Fatal("应保存签发的 client_id")
	}
	tok, err := m.Token(context.Background())
	if err != nil || tok != "at-1" {
		t.Fatalf("token: %q %v", tok, err)
	}

	// 快过期时刷新（轮换刷新令牌）；另一个 Manager（模拟另一个进程）读到的是刷新后的文件，不会重复刷新。
	clockMu.Lock()
	clock = clock.Add(59 * time.Minute)
	clockMu.Unlock()
	tok, err = m.Token(context.Background())
	if err != nil || tok != "at-2" {
		t.Fatalf("刷新后: %q %v", tok, err)
	}
	other := NewManager(dir, Options{Endpoints: f.endpoints(), Now: now})
	if tok, _ := other.Token(context.Background()); tok != "at-2" {
		t.Fatalf("另一个进程应直接使用刷新后的令牌: %q", tok)
	}
	if m.Status().Email != "me@example.com" {
		t.Fatal("刷新后应保留账号信息")
	}

	// 退出：吊销刷新令牌，删除凭据，保留注册。
	if err := m.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 1 || f.revoked[0] != "rt-2" {
		t.Fatalf("应吊销当前的刷新令牌: %v", f.revoked)
	}
	if m.LoggedIn() || !m.Status().Registered {
		t.Fatal("退出后应保留注册")
	}

	// 再次登录：复用签发的 client_id，不带 agent_name_hint，带 login_hint。
	authURL, _ = m.StartLogin(false)
	q = f.lastAuthAfter(t, authURL)
	if q.Get("client_id") != f.issued || q.Get("agent_name_hint") != "" || q.Get("login_hint") != "me@example.com" {
		t.Fatalf("再次登录参数: %v", q)
	}
	browse(t, authURL)
	waitState(t, m, StateLoggedIn)
}

// lastAuthAfter 解析授权地址的参数（与假服务器收到的一致）。
func (f *fakeAuth) lastAuthAfter(t *testing.T, authURL string) url.Values {
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestRefreshTokenReusedForcesRelogin(t *testing.T) {
	f := newFakeAuth(t)
	clock := time.Now()
	m := NewManager(t.TempDir(), Options{Endpoints: f.endpoints(), Now: func() time.Time { return clock }})
	authURL, _ := m.StartLogin(false)
	browse(t, authURL)
	waitState(t, m, StateLoggedIn)

	// 刷新令牌在别处被用掉了（服务器那边已作废）。
	f.mu.Lock()
	f.refresh = map[string]bool{}
	f.mu.Unlock()
	clock = clock.Add(2 * time.Hour)
	_, err := m.Token(context.Background())
	if err == nil || !strings.Contains(err.Error(), "重新登录") {
		t.Fatalf("应提示重新登录: %v", err)
	}
	st := m.Status()
	if st.State != StateLoggedOut || st.Error == "" {
		t.Fatalf("状态: %+v", st)
	}
}

func TestLoginErrors(t *testing.T) {
	f := newFakeAuth(t)
	m := NewManager(t.TempDir(), Options{Endpoints: f.endpoints()})

	// 用户拒绝授权。
	f.deny = true
	authURL, _ := m.StartLogin(false)
	code, page := browse(t, authURL)
	if code != 400 || !strings.Contains(page, "拒绝") {
		t.Fatalf("%d %s", code, page)
	}
	if st := waitState(t, m, StateLoggedOut); !strings.Contains(st.Error, "拒绝") {
		t.Fatalf("%+v", st)
	}

	// 回调没有带回签发的 client_id。
	f.deny, f.noIssued = false, true
	authURL, _ = m.StartLogin(false)
	browse(t, authURL)
	if st := waitState(t, m, StateLoggedOut); !strings.Contains(st.Error, "client_id") {
		t.Fatalf("%+v", st)
	}

	// 回调的 state 不对、Host 不对：拒绝，且不影响之后的正确回调。
	f.noIssued = false
	authURL, _ = m.StartLogin(false)
	q, _ := url.Parse(authURL)
	cb, _ := url.Parse(q.Query().Get("redirect_uri"))
	resp, _ := http.Get(cb.String() + "?state=wrong&code=x")
	if resp.StatusCode != 400 {
		t.Fatalf("state 不对应拒绝: %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ := http.NewRequest("GET", cb.String()+"?state=x", nil)
	req.Host = "localhost" + cb.Host[strings.LastIndex(cb.Host, ":"):]
	if resp, err := http.DefaultClient.Do(req); err == nil {
		if resp.StatusCode != 404 {
			t.Fatalf("Host 不对应拒绝: %d", resp.StatusCode)
		}
		resp.Body.Close()
	}
	browse(t, authURL)
	waitState(t, m, StateLoggedIn)

	// 取消正在进行的登录。
	_, _ = m.StartLogin(true)
	m.CancelLogin()
	if m.Status().State != StateLoggedIn {
		t.Fatal("取消新的登录不影响已有的登录")
	}
}

func TestVerifyIDTokenRejectsTampering(t *testing.T) {
	f := newFakeAuth(t)
	ctx := context.Background()
	d, err := fetchDiscovery(ctx, http.DefaultClient, f.endpoints())
	if err != nil {
		t.Fatal(err)
	}
	tok := f.idToken("n1")
	if _, err := verifyIDToken(ctx, http.DefaultClient, d, tok, f.issued, "n1", time.Now()); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(payload), "user-1", "user-2", 1))) + "." + parts[2]
	for name, c := range map[string]struct {
		tok, aud, nonce string
		now             time.Time
	}{
		"签名":    {forged, f.issued, "n1", time.Now()},
		"受众":    {tok, "oaiapp_other", "n1", time.Now()},
		"nonce": {tok, f.issued, "n2", time.Now()},
		"过期":    {tok, f.issued, "n1", time.Now().Add(3 * time.Hour)},
	} {
		if _, err := verifyIDToken(ctx, http.DefaultClient, d, c.tok, c.aud, c.nonce, c.now); err == nil {
			t.Errorf("%s 应校验失败", name)
		}
	}

	// 发现文档的签发者不一致。
	ep := f.endpoints()
	ep.Issuer = "https://evil.example"
	if _, err := fetchDiscovery(ctx, http.DefaultClient, ep); err == nil {
		t.Error("签发者不一致应报错")
	}
}

func TestParseTimeAndHostID(t *testing.T) {
	if parseTime(json.RawMessage(`1700000000`)).Unix() != 1700000000 ||
		parseTime(json.RawMessage(`1700000000000`)).Unix() != 1700000000 ||
		parseTime(json.RawMessage(`"2023-11-14T22:13:20Z"`)).Unix() != 1700000000 ||
		!parseTime(json.RawMessage(`null`)).IsZero() {
		t.Fatal("parseTime")
	}
	s := store{dir: t.TempDir()}
	a, _ := s.hostID()
	b, _ := s.hostID()
	if a != b || len(a) != len("urn:uuid:")+36 {
		t.Fatalf("host id 应稳定: %q %q", a, b)
	}
}

func TestUsageLedger(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	m := NewManager(dir, Options{Endpoints: DefaultEndpoints, Now: clock})

	m.ReportUsage("gpt-a", 100, 20, 30)
	now = now.Add(-6 * time.Hour) // 6 小时前的一次（只算进 7 天）
	m.ReportUsage("gpt-a", 1000, 200, 0)
	now = now.Add(6 * time.Hour)
	s := m.Usage()
	if s.Last5h.Requests != 1 || s.Last5h.InputTokens != 100 || s.Last5h.CachedTokens != 30 {
		t.Fatalf("5h: %+v", s.Last5h)
	}
	if s.Last7d.Requests != 2 || s.Last7d.InputTokens != 1100 || s.Last7d.OutputTokens != 220 {
		t.Fatalf("7d: %+v", s.Last7d)
	}

	// 额度用完：记录下来，之后成功调用一次就清除。
	now = now.Add(time.Minute)
	m.ReportLimit("plan limit")
	if s := m.Usage(); s.LimitHitAt.IsZero() || s.LimitMessage != "plan limit" {
		t.Fatalf("应记录额度用完: %+v", s)
	}
	now = now.Add(time.Minute)
	m.ReportUsage("gpt-a", 1, 1, 0)
	if s := m.Usage(); !s.LimitHitAt.IsZero() {
		t.Fatalf("成功调用后应清除: %+v", s)
	}

	// 另一个进程（新的 Manager）看到同一份记录；8 天后启动时过期记录被清理。
	if other := NewManager(dir, Options{Endpoints: DefaultEndpoints, Now: clock}); other.Usage().Last7d.Requests != 3 {
		t.Fatal("应读到同一份记录")
	}
	now = now.Add(8 * 24 * time.Hour)
	later := NewManager(dir, Options{Endpoints: DefaultEndpoints, Now: clock})
	if later.Usage().Last7d.Requests != 0 {
		t.Fatal("过期记录应清理")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "usage.jsonl"))
	if len(strings.TrimSpace(string(b))) != 0 {
		t.Fatalf("文件中的过期记录应删除: %q", b)
	}
}
