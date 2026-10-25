package settings

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/siwc"
)

// 模拟"已经登录"：直接写入一个还没过期的凭据文件，不走浏览器。
func seedCredential(t *testing.T, dir string) {
	t.Helper()
	b, _ := json.Marshal(siwc.Credential{ClientID: "oaiapp_x", Subject: "u1", Email: "me@example.com",
		AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: time.Now().Add(time.Hour)})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credential.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChatGPTProfile(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/responses" {
			io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"来自套餐额度的回答\"}\n\n"+
				"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":12,\"output_tokens\":5}}}\n\n")
			return
		}
		io.WriteString(w, `{"models":[{"slug":"gpt-m2"},{"slug":"gpt-m1"}]}`)
	}))
	defer srv.Close()
	ChatGPTBaseURL = srv.URL
	t.Cleanup(func() { ChatGPTBaseURL = "" })

	m, eng, path := newEnv(t)
	dir := filepath.Join(t.TempDir(), "chatgpt")
	c := siwc.NewManager(dir, siwc.Options{})
	m.AttachChatGPT(c)
	for _, v := range m.Providers() {
		if v.Name == ChatGPTProfile {
			t.Fatal("未登录时不应出现 chatgpt")
		}
	}

	// "登录"：写入凭据后触发状态变化回调（真实流程中由 siwc 在登录成功后调用）。
	seedCredential(t, dir)
	// 授权服务器指向一个不存在的本地地址：测试不应访问真实的 auth.openai.com（退出时会尝试吊销令牌）。
	dead := "http://127.0.0.1:1"
	c2 := siwc.NewManager(dir, siwc.Options{Endpoints: siwc.Endpoints{Issuer: dead, AuthorizeURL: dead, TokenURL: dead, DiscoveryURL: dead, Resource: dead}})
	m.AttachChatGPT(c2)
	m.onChatGPTChange()

	if gotAuth != "Bearer at-1" {
		t.Fatalf("获取模型列表应带上访问令牌: %q", gotAuth)
	}
	var view *ProviderView
	for _, v := range m.Providers() {
		if v.Name == ChatGPTProfile {
			view = &v
		}
	}
	if view == nil || view.Source != SourceAccount || view.Deletable || len(view.Models) != 2 || view.HasAPIKey || !view.Available {
		t.Fatalf("chatgpt 配置档: %+v", view)
	}
	if st := eng.Status(); st.Provider != ChatGPTProfile || st.Model != "gpt-m1" {
		t.Fatalf("应从演示厂商切换到 chatgpt: %+v", st)
	}
	// 用 chatgpt 发一轮对话：令牌由 siwc 提供，请求走 Responses API。
	res, err := eng.Send(context.Background(), "你好")
	if err != nil || res.Reason != "completed" {
		t.Fatalf("send: %+v %v", res, err)
	}
	if st := eng.Status(); st.Usage.InputTokens != 12 || st.Title == "" {
		t.Fatalf("应记录用量: %+v", st)
	}
	if st := m.ChatGPT(); st.State != siwc.StateLoggedIn || st.Email != "me@example.com" || st.ModelsError != "" {
		t.Fatalf("状态: %+v", st)
	}
	// 令牌不会出现在设置文件和界面视图里。
	b, _ := os.ReadFile(path)
	if json.Valid(b) && (strings.Contains(string(b), "at-1") || strings.Contains(string(b), "rt-1")) {
		t.Fatal("令牌不应写进 settings.json")
	}

	// 名称 chatgpt 保留。
	if err := m.Save(ProviderInput{Name: ChatGPTProfile, Type: "openai", BaseURL: "http://x/v1", Models: []string{"a"}}); err == nil {
		t.Fatal("chatgpt 名称应保留")
	}

	// 退出登录：配置档消失，模型列表保留在文件里。
	_ = m.LogoutChatGPT(context.Background()) // 吊销会失败（没有真的授权服务器），本地照样退出
	for _, v := range m.Providers() {
		if v.Name == ChatGPTProfile {
			t.Fatal("退出后不应出现 chatgpt")
		}
	}
	m2, _ := NewManager(path, nil, provider.DefaultRegistry)
	found := false
	for _, s := range m2.file.Providers {
		found = found || (s.Name == ChatGPTProfile && len(s.Models) == 2)
	}
	if !found {
		t.Fatal("模型列表应保留")
	}
}
