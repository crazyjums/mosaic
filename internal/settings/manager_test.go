package settings

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/provider"
	_ "github.com/crazyjums/myllmapp/internal/provider/all"
)

// newEnv 创建一个"只有 fake 演示厂商"的初始环境，模拟用户第一次打开应用。
func newEnv(t *testing.T, base ...provider.ProfileConfig) (*Manager, *core.Engine, string) {
	t.Helper()
	if len(base) == 0 {
		base = []provider.ProfileConfig{{Name: "fake", Type: "fake", Models: []string{"demo"}}}
	}
	path := filepath.Join(t.TempDir(), FileName)
	m, err := NewManager(path, base, provider.DefaultRegistry)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := core.NewEngine(core.EngineConfig{Catalog: m.Catalog()})
	if err != nil {
		t.Fatal(err)
	}
	m.Attach(eng)
	return m, eng, path
}

// fakeOpenAI 是一个最小的 OpenAI 兼容服务：/models 返回模型列表，/chat/completions 流式回复 "OK"。
func fakeOpenAI(t *testing.T, key string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			io.WriteString(w, `{"data":[{"id":"my-model-b"},{"id":"my-model-a"}]}`)
		case "/v1/chat/completions":
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSaveNewProviderSwitchesFromDemoAndPersists(t *testing.T) {
	srv := fakeOpenAI(t, "sk-secret-123456")
	m, eng, path := newEnv(t)

	in := ProviderInput{
		Name: "my-api", Type: "openai", BaseURL: srv.URL + "/v1/", APIKey: "sk-secret-123456",
		Models: []string{" my-model-a ", "my-model-b", "my-model-a"},
	}

	// 获取模型列表、测试连接都不保存任何东西。
	models, err := m.ListModels(context.Background(), in)
	if err != nil || len(models) != 2 {
		t.Fatalf("list models: %v %v", models, err)
	}
	if res := m.Test(context.Background(), in); !res.OK || res.Reply != "OK" || res.Model != "my-model-a" {
		t.Fatalf("test: %+v", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("测试连接不应写入设置文件")
	}

	if err := m.Save(in); err != nil {
		t.Fatal(err)
	}
	// 原来用的是演示厂商，保存后应自动切换到新厂商。
	if st := eng.Status(); st.Provider != "my-api" || st.Model != "my-model-a" {
		t.Fatalf("应自动切换到新配置的厂商: %+v", st)
	}

	// 写入了设置文件；重新加载后配置与选择都还在（模拟重启）。
	m2, err := NewManager(path, m.base, provider.DefaultRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if p, mod := m2.DefaultSelection(); p != "my-api" || mod != "my-model-a" {
		t.Fatalf("应记住选择: %s %s", p, mod)
	}
	var view ProviderView
	for _, v := range m2.Providers() {
		if v.Name == "my-api" {
			view = v
		}
	}
	if !view.Available || view.Source != SourceSettings || !view.Deletable || view.BaseURL != srv.URL+"/v1" {
		t.Fatalf("view: %+v", view)
	}
	if len(view.Models) != 2 {
		t.Fatalf("模型列表应去空格、去重: %v", view.Models)
	}
	// 视图里不能有密钥明文。
	if view.APIKeyHint != "sk-…3456" || strings.Contains(view.APIKeyHint, "secret") {
		t.Fatalf("密钥应被掩码: %q", view.APIKeyHint)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("设置文件权限应为 0600，实际 %v", fi.Mode().Perm())
		}
	}
}

func TestEditKeepsKeyAndRenames(t *testing.T) {
	srv := fakeOpenAI(t, "sk-secret-123456")
	m, eng, _ := newEnv(t)
	if err := m.Save(ProviderInput{Name: "a", Type: "openai", BaseURL: srv.URL + "/v1", APIKey: "sk-secret-123456", Models: []string{"m1"}}); err != nil {
		t.Fatal(err)
	}

	// 编辑时前端不回传密钥（拿不到明文），留空表示沿用。同时改名。
	if err := m.Save(ProviderInput{OriginalName: "a", Name: "b", Type: "openai", BaseURL: srv.URL + "/v1", Models: []string{"m1", "m2"}, DefaultModel: "m2"}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range m.Providers() {
		names = append(names, v.Name)
		if v.Name == "b" && !v.HasAPIKey {
			t.Fatal("编辑时留空密钥应沿用原密钥")
		}
	}
	if strings.Join(names, ",") != "fake,b" {
		t.Fatalf("改名后旧条目应被删除: %v", names)
	}
	if res := m.Test(context.Background(), ProviderInput{OriginalName: "b", Name: "b", Type: "openai", BaseURL: srv.URL + "/v1", Models: []string{"m1"}}); !res.OK {
		t.Fatalf("沿用的密钥应能通过测试: %+v", res)
	}
	if st := eng.Status(); st.Provider != "b" || st.Model != "m1" {
		t.Fatalf("当前厂商改名后应跟随，且保留正在用的模型: %+v", st)
	}

	// 再加一个厂商 c（当前不是演示厂商了，不应自动切换），然后把 b 改名为 d：当前应跟随到 d，而不是跳到 c。
	if err := m.Save(ProviderInput{Name: "c", Type: "openai", BaseURL: srv.URL + "/v1", APIKey: "sk-secret-123456", Models: []string{"m9"}}); err != nil {
		t.Fatal(err)
	}
	if st := eng.Status(); st.Provider != "b" {
		t.Fatalf("已在使用真实厂商时，新增厂商不应自动切换: %+v", st)
	}
	if err := m.Save(ProviderInput{OriginalName: "b", Name: "d", Type: "openai", BaseURL: srv.URL + "/v1", Models: []string{"m1"}}); err != nil {
		t.Fatal(err)
	}
	if st := eng.Status(); st.Provider != "d" {
		t.Fatalf("正在使用的厂商改名后应跟随到新名字: %+v", st)
	}
}

func TestValidation(t *testing.T) {
	m, _, _ := newEnv(t)
	cases := map[string]ProviderInput{
		"缺名称":           {Type: "openai", Models: []string{"x"}},
		"缺模型":           {Name: "x", Type: "openai"},
		"未知类型":          {Name: "x", Type: "gemini", Models: []string{"x"}},
		"URL 无协议":       {Name: "x", Type: "openai", BaseURL: "api.example.com", Models: []string{"x"}},
		"重名":            {Name: "fake", Type: "openai", Models: []string{"x"}},
		"Anthropic 缺密钥": {Name: "x", Type: "anthropic", Models: []string{"claude-opus-5-5"}},
	}
	for name, in := range cases {
		if err := m.Save(in); err == nil {
			t.Errorf("%s: 应校验失败", name)
		}
	}
}

func TestOverrideConfigProfileAndDelete(t *testing.T) {
	// 配置文件里有一个 deepseek，密钥来自环境变量。
	base := []provider.ProfileConfig{
		{Name: "fake", Type: "fake", Models: []string{"demo"}},
		{Name: "deepseek", Type: "openai", BaseURL: "https://api.deepseek.com/v1", APIKey: "from-env-xxxxxxxx", Models: []string{"deepseek-chat"}},
	}
	m, _, path := newEnv(t, base...)

	// 在界面上只修改模型列表（不填密钥）：应形成覆盖，并保留环境变量里的密钥。
	if err := m.Save(ProviderInput{OriginalName: "deepseek", Name: "deepseek", Type: "openai", BaseURL: "https://api.deepseek.com/v1", Models: []string{"deepseek-chat", "deepseek-reasoner"}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Profiles() {
		if p.Name == "deepseek" && (p.APIKey != "from-env-xxxxxxxx" || len(p.Models) != 2) {
			t.Fatalf("覆盖应保留原密钥: %+v", p)
		}
	}
	// 沿用的密钥来自环境变量，不能被明文写进设置文件。
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "from-env-xxxxxxxx") {
		t.Fatalf("环境变量中的密钥不应写入设置文件: %s", b)
	}
	for _, v := range m.Providers() {
		if v.Name == "deepseek" && v.Source != SourceOverride {
			t.Fatalf("source: %s", v.Source)
		}
		if v.Name == "fake" && v.Deletable {
			t.Fatal("配置文件中的配置档不可删除")
		}
	}

	// 删除覆盖后回到配置文件中的版本；配置文件中的配置档本身删不掉。
	if err := m.Delete("deepseek"); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Profiles() {
		if p.Name == "deepseek" && len(p.Models) != 1 {
			t.Fatalf("删除覆盖后应恢复配置文件中的版本: %+v", p)
		}
	}
	if err := m.Delete("fake"); err == nil {
		t.Fatal("配置文件中的配置档不应能删除")
	}
}

func TestSelectRemembersChoice(t *testing.T) {
	srv := fakeOpenAI(t, "k-1234567890")
	m, _, path := newEnv(t)
	_ = m.Save(ProviderInput{Name: "x", Type: "openai", BaseURL: srv.URL + "/v1", APIKey: "k-1234567890", Models: []string{"m1", "m2"}})
	if err := m.Select("x", "m2"); err != nil {
		t.Fatal(err)
	}
	f, _ := Load(path)
	if f.DefaultProvider != "x" || f.DefaultModel != "m2" {
		t.Fatalf("应记住选择: %+v", f)
	}
	if err := m.Select("nope", ""); err == nil {
		t.Fatal("不存在的配置档应报错")
	}
}

func TestLocalServerWithoutKey(t *testing.T) {
	srv := fakeOpenAI(t, "")
	m, _, _ := newEnv(t)
	// Ollama 之类的本地服务不需要密钥。
	if err := m.Save(ProviderInput{Name: "ollama", Type: "openai", BaseURL: srv.URL + "/v1", Models: []string{"qwen"}}); err != nil {
		t.Fatalf("无密钥的 OpenAI 兼容服务应能保存: %v", err)
	}
}

func TestPermissionModeAndStatusLinePersist(t *testing.T) {
	m, eng, path := newEnv(t)
	if err := m.SetPermissionMode("accept_edits"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPermissionMode("auto"); err != nil {
		t.Fatal(err)
	}
	if eng.Status().PermissionMode != "auto" {
		t.Fatal("权限模式应立即生效")
	}
	if err := m.SetPermissionMode("yolo"); err == nil {
		t.Fatal("未知模式应报错")
	}

	if string(m.StatusLine()) != "null" {
		t.Fatal("从未保存时应返回 null")
	}
	if err := m.SaveStatusLine([]byte(`[1,2]`)); err == nil {
		t.Fatal("非对象应报错")
	}
	if err := m.SaveStatusLine([]byte(`{"version":1,"items":[{"id":"git","enabled":false}]}`)); err != nil {
		t.Fatal(err)
	}

	// 重新加载（模拟重启）后都还在。
	m2, _ := NewManager(path, m.base, provider.DefaultRegistry)
	// auto 只对本次运行生效，记住的是切换前的档位。
	if m2.PermissionMode() != "accept_edits" || !strings.Contains(string(m2.StatusLine()), `"git"`) {
		t.Fatalf("应持久化（auto 除外）: %q %s", m2.PermissionMode(), m2.StatusLine())
	}
}
