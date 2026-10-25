package codexquota

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeAuth(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGetParsesAndCaches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer at-1" || r.Header.Get("ChatGPT-Account-Id") != "acc-9" {
			w.WriteHeader(401)
			return
		}
		// 只有每周窗口的套餐会把它放在 primary；这里故意把长窗口放在 primary，测试按长度排序。
		io.WriteString(w, `{"plan_type":"plus","rate_limit":{
			"primary_window":{"used_percent":3,"limit_window_seconds":604800,"reset_at":1761900000},
			"secondary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":600}},
			"credits":{"has_credits":false,"balance":"0"}}`)
	}))
	defer srv.Close()

	// account_id 不在 tokens 里时，从 ID 令牌的声明里取。
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc-9"}}`))
	auth := writeAuth(t, `{"tokens":{"access_token":"at-1","refresh_token":"rt","id_token":"h.`+claims+`.s"}}`)
	now := time.Unix(1761800000, 0)
	c := &Client{AuthPath: auth, URL: srv.URL, Now: func() time.Time { return now }}

	q, err := c.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if q.Plan != "plus" || len(q.Windows) != 2 || q.Windows[0].Seconds != 18000 || q.Windows[1].UsedPercent != 3 {
		t.Fatalf("解析: %+v", q)
	}
	if !q.Windows[0].ResetAt.Equal(now.Add(10*time.Minute)) || q.Windows[1].ResetAt.Unix() != 1761900000 {
		t.Fatalf("重置时间: %+v", q.Windows)
	}
	if q.Credits == nil || *q.Credits != 0 {
		t.Fatalf("credits: %v", q.Credits)
	}

	// 一分钟内走缓存；过期或 Invalidate 后重新请求。
	c.Get(context.Background())
	if calls != 1 {
		t.Fatalf("应使用缓存，实际请求 %d 次", calls)
	}
	c.Invalidate()
	c.Get(context.Background())
	if calls != 2 {
		t.Fatal("Invalidate 后应重新请求")
	}

	// 令牌不会被改写（不替 Codex 刷新）。
	b, _ := os.ReadFile(auth)
	if !strings.Contains(string(b), `"refresh_token":"rt"`) {
		t.Fatal("不应修改 Codex 的登录信息")
	}
}

func TestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	cases := map[string]struct {
		auth string
		want string
	}{
		"没有登录文件":     {"", "codex login"},
		"API Key 登录": {`{"OPENAI_API_KEY":"sk-x","tokens":null}`, "不是用 ChatGPT 账号登录"},
		"令牌过期":       {`{"tokens":{"access_token":"old","account_id":"a"}}`, "已过期"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing.json")
			if tc.auth != "" {
				path = writeAuth(t, tc.auth)
			}
			_, err := (&Client{AuthPath: path, URL: srv.URL}).Get(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestCodexHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	p, _ := (&Client{}).authPath()
	if p != filepath.Join(dir, "auth.json") {
		t.Fatal(p)
	}
}
