package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateAndRedactProxy(t *testing.T) {
	for _, ok := range []string{"", "direct", "http://127.0.0.1:7890", "https://p.example:443", "socks5://u:p@127.0.0.1:1080", "socks5h://h:1"} {
		if err := ValidateProxy(ok); err != nil {
			t.Errorf("%q 应合法: %v", ok, err)
		}
	}
	for _, bad := range []string{"127.0.0.1:7890", "ftp://h:1", "http://", "://x", "socks4://h:1"} {
		if err := ValidateProxy(bad); err == nil {
			t.Errorf("%q 应不合法", bad)
		}
	}
	if got := RedactProxy("http://alice:s3cret@proxy.local:8080"); got != "http://alice:***@proxy.local:8080" {
		t.Fatalf("密码必须脱敏: %q", got)
	}
	if strings.Contains(RedactProxy("http://alice:s3cret@proxy.local:8080"), "s3cret") || strings.Contains(RedactProxy("::bad::s3cret"), "s3cret") {
		t.Fatal("脱敏后不能含密码")
	}
	if RedactProxy("direct") != "direct" || RedactProxy("") != "" || RedactProxy("http://h:1") != "http://h:1" {
		t.Fatal("无密码的原样返回")
	}
	// 校验错误信息本身也不能带出密码。
	if err := ValidateProxy("ftp://u:topsecret@h:1"); err != nil && strings.Contains(err.Error(), "topsecret") {
		t.Fatalf("错误信息泄露了密码: %v", err)
	}
}

func TestClientUsesConfiguredProxy(t *testing.T) {
	var seen string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.String() // 经代理的请求，路径是完整的绝对地址
		io.WriteString(w, "via-proxy")
	}))
	defer proxy.Close()

	c, err := NewHTTPClient(ProfileConfig{Options: map[string]any{"proxy": proxy.URL}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get("http://upstream.example.test/v1/chat")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "via-proxy" || seen != "http://upstream.example.test/v1/chat" {
		t.Fatalf("请求应经过代理: %q %q", b, seen)
	}
}

func TestDirectIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy.example:3128")
	t.Setenv("HTTP_PROXY", "http://env-proxy.example:3128")
	req, _ := http.NewRequest("GET", "https://api.example.com/x", nil)

	env, _ := NewHTTPClient(ProfileConfig{})
	if u, _ := env.Transport.(*http.Transport).Proxy(req); u == nil || u.Host != "env-proxy.example:3128" {
		t.Fatalf("留空应沿用环境变量: %v", u)
	}
	direct, _ := NewHTTPClient(ProfileConfig{Options: map[string]any{"proxy": "direct"}})
	if direct.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("direct 应强制直连")
	}
	if _, err := NewHTTPClient(ProfileConfig{Options: map[string]any{"proxy": "nonsense"}}); err == nil {
		t.Fatal("非法代理应报错")
	}
	socks, err := NewHTTPClient(ProfileConfig{Options: map[string]any{"proxy": "socks5://127.0.0.1:1080"}})
	if err != nil || socks.Transport.(*http.Transport).Proxy != nil {
		t.Fatalf("socks5 应通过自定义拨号实现: %v", err)
	}
}
