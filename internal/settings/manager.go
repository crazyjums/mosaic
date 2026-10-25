package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/codexquota"
	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/secret"
	"github.com/crazyjums/myllmapp/internal/siwc"
)

// ProviderView 是发给前端的配置档视图。注意：不含 API Key 明文。
type ProviderView struct {
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	BaseURL      string         `json:"base_url"`
	Models       []string       `json:"models"`
	DefaultModel string         `json:"default_model"`
	Options      map[string]any `json:"options"`
	HasAPIKey    bool           `json:"has_api_key"`
	// APIKeyHint 是掩码后的密钥，例如 "sk-…a1b2"，让用户确认填的是哪一个。
	APIKeyHint string `json:"api_key_hint"`
	Source     Source `json:"source"`
	// Deletable 为 false 表示配置档来自 config.toml，界面上删不掉（只能删除覆盖）。
	Deletable bool   `json:"deletable"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

// ProviderInput 是前端提交的表单。
type ProviderInput struct {
	// OriginalName 是编辑前的名称；新建时为空。名称被修改时据此删除旧条目。
	OriginalName string         `json:"original_name"`
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	BaseURL      string         `json:"base_url"`
	APIKey       string         `json:"api_key"`
	Models       []string       `json:"models"`
	DefaultModel string         `json:"default_model"`
	Options      map[string]any `json:"options"`
}

// TestResult 是"测试连接"的结果。
type TestResult struct {
	OK        bool   `json:"ok"`
	Model     string `json:"model"`
	LatencyMS int64  `json:"latency_ms"`
	Reply     string `json:"reply,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Manager 管理界面上的厂商配置。所有方法并发安全。
type Manager struct {
	mu       sync.Mutex
	path     string
	file     File
	base     []provider.ProfileConfig // 来自 config.toml / 内置默认配置，密钥已解析
	registry *provider.Registry
	engine   *core.Engine
	// rtBase 是常规设置（运行参数）的默认值与钩子，见 general.go。注意与上面的 base（厂商配置档）区分。
	rtBase RuntimeBase

	// 钥匙串，见 keychain.go。secrets 为 nil 表示不支持（CLI 测试、无钥匙串的环境）。
	secrets    secret.Store
	inKeychain map[string]bool // 当前保存在钥匙串里的配置档名
	unresolved map[string]bool // 启动时没能从钥匙串读出密钥的配置档名
	keyWarning string

	// ChatGPT 账号登录，见 chatgpt.go。
	chatgpt          *siwc.Manager
	chatgptModelsErr string
	codexQuota       *codexquota.Client // 剩余额度查询（可选，见 chatgpt.go）
}

// NewManager 读取设置文件。path 为 settings.json 的路径；base 为配置文件中的配置档。不使用钥匙串。
func NewManager(path string, base []provider.ProfileConfig, reg *provider.Registry) (*Manager, error) {
	return NewManagerWithSecrets(path, base, reg, nil)
}

// NewManagerWithSecrets 同 NewManager，并用 secrets 读写保存在钥匙串里的密钥。
func NewManagerWithSecrets(path string, base []provider.ProfileConfig, reg *provider.Registry, secrets secret.Store) (*Manager, error) {
	f, err := Load(path)
	if err != nil {
		return nil, err
	}
	m := &Manager{path: path, file: f, base: base, registry: reg, secrets: secrets}
	m.resolveSecrets()
	return m, nil
}

// Attach 关联 Engine。Engine 需要用 Catalog() 创建，所以只能在 Manager 之后创建、再关联回来。
func (m *Manager) Attach(e *core.Engine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.engine = e
}

// Profiles 返回合并后的配置档（含密钥，只在后端使用）。
func (m *Manager) Profiles() []provider.ProfileConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	ps, _ := m.merged(m.file)
	return ps
}

// Catalog 用合并后的配置档创建厂商目录。
func (m *Manager) Catalog() *provider.Catalog {
	return provider.NewCatalog(m.registry, m.Profiles())
}

// DefaultSelection 返回上次在界面上选择的厂商和模型（可能为空）。
func (m *Manager) DefaultSelection() (profile, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.file.DefaultProvider, m.file.DefaultModel
}

// Providers 返回所有配置档的界面视图。
func (m *Manager) Providers() []ProviderView {
	m.mu.Lock()
	ps, sources := m.merged(m.file)
	m.mu.Unlock()

	infos := map[string]provider.ProfileInfo{}
	for _, info := range provider.NewCatalog(m.registry, ps).Profiles() {
		infos[info.Name] = info
	}
	out := make([]ProviderView, 0, len(ps))
	for _, p := range ps {
		info := infos[p.Name]
		out = append(out, ProviderView{
			Name:         p.Name,
			Type:         p.Type,
			BaseURL:      p.BaseURL,
			Models:       nonNil(p.Models),
			DefaultModel: p.DefaultModel,
			Options:      p.Options,
			HasAPIKey:    p.APIKey != "",
			APIKeyHint:   maskKey(p.APIKey),
			Source:       sources[p.Name],
			Deletable:    sources[p.Name] != SourceConfig && sources[p.Name] != SourceAccount,
			Available:    info.Available,
			Error:        info.Error,
		})
	}
	return out
}

// Save 新建或修改一个配置档，立即生效并写入设置文件。
//
// 步骤：校验 → 生成新的设置 → 用新设置创建目录并替换 Engine 中的目录 → 写文件。
// 先替换后写文件：如果 Engine 正忙（ErrBusy），什么都不改；反过来则会出现"文件改了、运行中的没改"。
func (m *Manager) Save(in ProviderInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 步骤 1：校验与规范化。normalize 会在密钥留空时填入沿用的密钥（用于下面的校验），
	// 但写进设置文件的密钥要单独决定，见 keyToPersist。
	typedKey := strings.TrimSpace(in.APIKey)
	in, err := m.normalize(in)
	if err != nil {
		return err
	}
	setting := ProviderSetting{
		Name:         in.Name,
		Type:         in.Type,
		BaseURL:      in.BaseURL,
		APIKey:       in.APIKey,
		Models:       in.Models,
		DefaultModel: in.DefaultModel,
		Options:      in.Options,
	}
	// 能不能真正创建出实例？例如 Anthropic 缺密钥会在这里报错，比保存后再在下拉框里置灰更直观。
	if _, err := m.registry.New(m.withAuth(toProfile(setting))); err != nil {
		return err
	}
	setting.APIKey = m.keyToPersist(typedKey, in)

	// 步骤 2：生成新的设置文件内容（改名时去掉旧条目）。
	next := cloneFile(m.file)
	next.Providers = slices.DeleteFunc(next.Providers, func(s ProviderSetting) bool {
		return s.Name == in.Name || (in.OriginalName != "" && s.Name == in.OriginalName)
	})
	next.Providers = append(next.Providers, setting)
	if in.OriginalName != "" && in.OriginalName != in.Name && next.DefaultProvider == in.OriginalName {
		next.DefaultProvider, next.DefaultModel = in.Name, in.DefaultModel
	}

	// 步骤 3：替换 Engine 中的目录。
	// 正在使用的就是被编辑的配置档时，继续使用它（改名后跟随新名字，模型被删掉则换成默认模型）；
	// 当前还在用演示厂商时，顺手切到刚配置好的厂商。
	editingCurrent, currentModel := false, ""
	if m.engine != nil {
		st := m.engine.Status()
		editingCurrent = st.Provider == in.Name || (in.OriginalName != "" && st.Provider == in.OriginalName)
		currentModel = st.Model
	}
	if err := m.apply(next); err != nil {
		return err
	}
	if m.engine != nil && (editingCurrent || m.currentIsFake()) {
		model := in.DefaultModel
		if editingCurrent && slices.Contains(in.Models, currentModel) {
			model = currentModel
		}
		if err := m.engine.SetModel(in.Name, model); err == nil {
			st := m.engine.Status()
			next.DefaultProvider, next.DefaultModel = st.Provider, st.Model
		}
	}

	// 步骤 4：持久化。
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

// Delete 删除界面上配置的配置档（或删除对 config.toml 配置档的覆盖）。
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.ContainsFunc(m.file.Providers, func(s ProviderSetting) bool { return s.Name == name }) {
		return fmt.Errorf("配置档 %q 来自配置文件，不能在界面中删除（请编辑 config.toml）", name)
	}
	next := cloneFile(m.file)
	next.Providers = slices.DeleteFunc(next.Providers, func(s ProviderSetting) bool { return s.Name == name })
	if next.DefaultProvider == name {
		next.DefaultProvider, next.DefaultModel = "", ""
	}
	if err := m.apply(next); err != nil {
		return err
	}
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

// Select 切换当前使用的厂商和模型，并记住选择，下次启动直接使用。
func (m *Manager) Select(profile, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine == nil {
		return errors.New("settings: engine not attached")
	}
	if err := m.engine.SetModel(profile, model); err != nil {
		return err
	}
	st := m.engine.Status()
	if m.file.DefaultProvider == st.Provider && m.file.DefaultModel == st.Model {
		return nil
	}
	next := cloneFile(m.file)
	next.DefaultProvider, next.DefaultModel = st.Provider, st.Model
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

// PermissionMode 返回记住的权限模式（可能为空）。
func (m *Manager) PermissionMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.file.PermissionMode
}

// SetPermissionMode 切换权限档位并记住，下次启动直接使用。
//
// 例外是 auto（全部放行）：只对本次运行生效，不写入设置文件，文件里保留切换前记住的档位。
// 否则某次临时开了全部放行，之后每次启动都会在不知不觉中不经确认地执行命令。
func (m *Manager) SetPermissionMode(mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine == nil {
		return errors.New("settings: engine not attached")
	}
	if err := m.engine.SetPermissionMode(mode); err != nil {
		return err
	}
	if permission.Mode(mode) == permission.ModeAuto || m.file.PermissionMode == mode {
		return nil
	}
	next := cloneFile(m.file)
	next.PermissionMode = mode
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

// maxOpaqueSize 限制前端不透明配置（状态栏、界面偏好）的大小，防止前端的 bug 把设置文件写得巨大。
const maxOpaqueSize = 64 << 10

// StatusLine 返回保存的状态栏配置（JSON 对象）；从未保存过时返回 null，前端使用默认配置。
func (m *Manager) StatusLine() json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return orNull(m.file.StatusLine)
}

// SaveStatusLine 保存状态栏配置。只校验"是一个 JSON 对象"，字段含义由前端定义。
func (m *Manager) SaveStatusLine(raw json.RawMessage) error {
	return m.saveOpaque(raw, "statusline", func(f *File, v json.RawMessage) { f.StatusLine = v })
}

func (m *Manager) saveOpaque(raw json.RawMessage, what string, set func(*File, json.RawMessage)) error {
	if len(raw) > maxOpaqueSize {
		return fmt.Errorf("settings: %s config too large (%d bytes)", what, len(raw))
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return fmt.Errorf("settings: %s config must be a JSON object", what)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneFile(m.file)
	set(&next, append(json.RawMessage(nil), raw...))
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

func orNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

// Test 用表单中的配置发一条极短的消息，验证 Base URL、密钥和模型名是否都正确。
// 不保存任何东西。会产生一次真实调用（几个 token 的费用）。
func (m *Manager) Test(ctx context.Context, in ProviderInput) TestResult {
	m.mu.Lock()
	in, err := m.normalize(in)
	m.mu.Unlock()
	if err != nil {
		return TestResult{Error: err.Error()}
	}
	model := in.DefaultModel
	if model == "" {
		return TestResult{Error: "请先填写至少一个模型"}
	}
	m.mu.Lock()
	cfg := m.withAuth(toProfile(ProviderSetting{
		Name: in.Name, Type: in.Type, BaseURL: in.BaseURL, APIKey: in.APIKey, Options: in.Options,
	}))
	m.mu.Unlock()
	p, err := m.registry.New(cfg)
	if err != nil {
		return TestResult{Model: model, Error: err.Error()}
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	start := time.Now()
	var reply strings.Builder
	err = p.Stream(ctx, provider.Request{
		Model:     model,
		Messages:  []provider.Message{provider.UserText("This is a connectivity test. Reply with exactly: OK")},
		MaxTokens: 1024,
	}, func(d provider.Delta) {
		if d.Type == provider.DeltaText && reply.Len() < 200 {
			reply.WriteString(d.Text)
		}
	})
	res := TestResult{Model: model, LatencyMS: time.Since(start).Milliseconds(), Reply: strings.TrimSpace(reply.String())}
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	return res
}

// ListModels 用表单中的 Base URL 和密钥从服务器获取模型列表。
func (m *Manager) ListModels(ctx context.Context, in ProviderInput) ([]string, error) {
	m.mu.Lock()
	in, err := m.normalizeConnection(in)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	cfg := m.withAuth(toProfile(ProviderSetting{
		Name: in.Name, Type: in.Type, BaseURL: in.BaseURL, APIKey: in.APIKey, Options: in.Options,
	}))
	m.mu.Unlock()
	p, err := m.registry.New(cfg)
	if err != nil {
		return nil, err
	}
	lister, ok := p.(provider.ModelLister)
	if !ok {
		return nil, fmt.Errorf("%s 类型不支持获取模型列表，请手动填写", in.Type)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return lister.ListModels(ctx)
}

// ---- 内部辅助（调用方持有 m.mu） ----

// normalizeConnection 规范化与"连接"相关的字段：类型、Base URL、密钥。
// 密钥留空表示"沿用已保存的密钥"——编辑时前端拿不到明文，也就不会回传。
func (m *Manager) normalizeConnection(in ProviderInput) (ProviderInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Type = strings.TrimSpace(in.Type)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.APIKey = strings.TrimSpace(in.APIKey)
	if !slices.Contains(m.registry.Adapters(), in.Type) {
		return in, fmt.Errorf("不支持的协议类型 %q", in.Type)
	}
	if in.BaseURL != "" && !strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://") {
		return in, fmt.Errorf("Base URL 需要以 http:// 或 https:// 开头")
	}
	if in.APIKey == "" {
		in.APIKey = m.existingKey(in.OriginalName, in.Name)
	}
	if in.Options == nil {
		in.Options = map[string]any{}
	}
	// OpenAI 兼容的本地服务（Ollama、vLLM、LM Studio）通常不需要密钥。
	if in.Type == "openai" && in.APIKey == "" {
		in.Options["allow_no_key"] = true
	} else {
		delete(in.Options, "allow_no_key")
	}
	return in, nil
}

// normalize 在 normalizeConnection 之外，再校验名称和模型列表。
func (m *Manager) normalize(in ProviderInput) (ProviderInput, error) {
	in, err := m.normalizeConnection(in)
	if err != nil {
		return in, err
	}
	if in.Name == "" {
		return in, errors.New("请填写名称")
	}
	if in.Name == ChatGPTProfile && in.Type != "chatgpt" {
		return in, fmt.Errorf("名称 %q 保留给 ChatGPT 账号登录，请换一个名称", ChatGPTProfile)
	}
	var models []string
	for _, x := range in.Models {
		if x = strings.TrimSpace(x); x != "" && !slices.Contains(models, x) {
			models = append(models, x)
		}
	}
	in.Models = models
	in.DefaultModel = strings.TrimSpace(in.DefaultModel)
	if len(in.Models) == 0 {
		return in, errors.New("请至少填写一个模型（可以点击“获取模型列表”）")
	}
	if in.DefaultModel == "" || !slices.Contains(in.Models, in.DefaultModel) {
		in.DefaultModel = in.Models[0]
	}
	// 改名或新建时，名字不能与其他配置档冲突。
	if in.Name != in.OriginalName {
		ps, _ := m.merged(m.file)
		if slices.ContainsFunc(ps, func(p provider.ProfileConfig) bool { return p.Name == in.Name }) {
			return in, fmt.Errorf("名称 %q 已存在", in.Name)
		}
	}
	return in, nil
}

// existingKey 查找已保存的密钥（先按原名称，再按新名称）。
func (m *Manager) existingKey(names ...string) string {
	ps, _ := m.merged(m.file)
	for _, n := range names {
		if n == "" {
			continue
		}
		for _, p := range ps {
			if p.Name == n && p.APIKey != "" {
				return p.APIKey
			}
		}
	}
	return ""
}

// keyToPersist 决定写进设置文件的密钥。
//
// 留空沿用的密钥如果来自配置文件（常见的是 api_key_env 指向的环境变量），不能写进 settings.json：
// 那样会把环境变量里的密钥明文落盘，而且之后用户轮换环境变量也不再生效（设置文件里的优先）。
// 同名的配置文件配置档存在时留空即可，merge 会沿用它的密钥。
func (m *Manager) keyToPersist(typed string, in ProviderInput) string {
	if typed != "" {
		return typed
	}
	for _, n := range []string{in.OriginalName, in.Name} {
		for _, s := range m.file.Providers {
			if n != "" && s.Name == n && s.APIKey != "" {
				return s.APIKey
			}
		}
	}
	if slices.ContainsFunc(m.base, func(p provider.ProfileConfig) bool { return p.Name == in.Name }) {
		return ""
	}
	// 例如把配置文件里的配置档改名另存：新名字没有可以沿用的来源，只能保存一份。
	return in.APIKey
}

// apply 用新的设置重建厂商目录并交给 Engine。
func (m *Manager) apply(next File) error {
	if m.engine == nil {
		return nil
	}
	ps, _ := m.merged(next)
	return m.engine.ReplaceCatalog(provider.NewCatalog(m.registry, ps))
}

// currentIsFake 判断当前会话是否还在用演示厂商。
func (m *Manager) currentIsFake() bool {
	st := m.engine.Status()
	for _, p := range m.engine.Profiles() {
		if p.Name == st.Provider {
			return p.Type == "fake"
		}
	}
	return false
}

func cloneFile(f File) File {
	f.Providers = slices.Clone(f.Providers)
	f.PermissionRules = slices.Clone(f.PermissionRules)
	f.DisabledTools = slices.Clone(f.DisabledTools)
	f.MCPServers = slices.Clone(f.MCPServers)
	return f
}

// maskKey 只露出密钥的前 3 位和后 4 位。
func maskKey(k string) string {
	switch {
	case k == "":
		return ""
	case len(k) <= 10:
		return "••••••"
	default:
		return k[:3] + "…" + k[len(k)-4:]
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
