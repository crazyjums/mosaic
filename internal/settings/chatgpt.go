package settings

import (
	"context"
	"slices"
	"time"

	"github.com/crazyjums/myllmapp/internal/codexquota"
	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/siwc"
)

// 用 ChatGPT 账号登录（设置 → 模型厂商 → ChatGPT 账号）。
//
// 登录本身在 internal/siwc；这里负责把登录状态变成一个厂商配置档：
//   - 登录后出现一个名为 chatgpt、类型为 chatgpt 的配置档，它的 Auth 就是 siwc.Manager（按需刷新令牌）；
//   - 模型列表和其他厂商一样保存在 settings.json 的 providers 里（首次登录时自动从服务器获取），可以在界面上编辑；
//   - 退出登录后配置档消失，但保存的模型列表保留，下次登录直接用。

// ChatGPTProfile 是登录后出现的配置档名称。
const ChatGPTProfile = "chatgpt"

// ChatGPTBaseURL 是 chatgpt 配置档的 API 地址；为空时使用适配器的默认值（https://api.openai.com/v1）。
// 只为测试而导出。
var ChatGPTBaseURL = ""

// SourceAccount 表示配置档来自账号登录（不能删除，只能退出登录）。
const SourceAccount Source = "account"

// ChatGPTStatus 是设置页显示的 ChatGPT 账号状态。
type ChatGPTStatus struct {
	siwc.Status
	// ModelsError 非空表示登录后没能自动获取模型列表，需要手动填写或点"获取模型列表"。
	ModelsError string `json:"models_error,omitempty"`
}

// ChatGPTUsageURL 是 ChatGPT 的用量页面。OpenAI 目前没有提供查询剩余额度的接口，只能让用户在这里查看。
const ChatGPTUsageURL = "https://chatgpt.com/settings/usage"

// ChatGPTUsage 是状态栏"ChatGPT 额度"显示的数据。
//   - 本应用用掉的套餐额度（总是有，见 siwc/usage.go）；
//   - 剩余额度（可选，开启"读取 Codex 登录查询剩余额度"后才有，见 internal/codexquota）。
type ChatGPTUsage struct {
	LoggedIn bool `json:"logged_in"`
	siwc.UsageSummary
	UsageURL string `json:"usage_url"`

	QuotaEnabled bool              `json:"quota_enabled"`
	Quota        *codexquota.Quota `json:"quota,omitempty"`
	QuotaError   string            `json:"quota_error,omitempty"`
}

// ChatGPTUsage 返回本应用用掉的套餐额度；开启了剩余额度查询时一并返回（有一分钟缓存）。
func (m *Manager) ChatGPTUsage(ctx context.Context) ChatGPTUsage {
	m.mu.Lock()
	c, enabled := m.chatgpt, m.file.CodexQuota
	if m.codexQuota == nil {
		m.codexQuota = &codexquota.Client{}
	}
	q := m.codexQuota
	m.mu.Unlock()
	out := ChatGPTUsage{UsageURL: ChatGPTUsageURL, QuotaEnabled: enabled}
	if c != nil {
		out.LoggedIn, out.UsageSummary = c.LoggedIn(), c.Usage()
	}
	if enabled {
		if quota, err := q.Get(ctx); err != nil {
			out.QuotaError = err.Error()
		} else {
			out.Quota = quota
		}
	}
	return out
}

// SetCodexQuota 开启或关闭"读取 Codex 登录查询剩余额度"。
func (m *Manager) SetCodexQuota(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneFile(m.file)
	next.CodexQuota = on
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	if m.codexQuota != nil {
		m.codexQuota.Invalidate()
	}
	return nil
}

// AttachChatGPT 关联登录管理器。必须在创建厂商目录（Catalog）之前调用，这样启动时就能选中已登录的 chatgpt。
func (m *Manager) AttachChatGPT(c *siwc.Manager) {
	m.mu.Lock()
	m.chatgpt = c
	m.mu.Unlock()
	c.OnChange(m.onChatGPTChange)
}

// merged 合并配置文件、设置文件和账号登录的配置档。调用方持有 m.mu。
func (m *Manager) merged(f File) ([]provider.ProfileConfig, map[string]Source) {
	loggedIn := m.chatgpt != nil && m.chatgpt.LoggedIn()
	base := m.base
	if loggedIn && !slices.ContainsFunc(base, func(p provider.ProfileConfig) bool { return p.Name == ChatGPTProfile }) {
		base = append(slices.Clone(base), provider.ProfileConfig{Name: ChatGPTProfile, Type: "chatgpt", BaseURL: ChatGPTBaseURL})
	}
	if !loggedIn {
		// 没登录时隐藏 chatgpt 配置档（保存的模型列表留在文件里，下次登录直接用）。
		f.Providers = slices.DeleteFunc(slices.Clone(f.Providers), func(s ProviderSetting) bool { return s.Type == "chatgpt" })
	}
	ps, sources := merge(base, f)
	for i := range ps {
		if ps[i].Type == "chatgpt" {
			if ps[i].BaseURL == "" {
				ps[i].BaseURL = ChatGPTBaseURL // 设置文件里保存的模型列表不带地址
			}
			ps[i] = m.withAuth(ps[i])
			sources[ps[i].Name] = SourceAccount
		}
	}
	return ps, sources
}

// withAuth 给 chatgpt 类型的配置档注入令牌来源。
func (m *Manager) withAuth(p provider.ProfileConfig) provider.ProfileConfig {
	if p.Type == "chatgpt" && m.chatgpt != nil && m.chatgpt.LoggedIn() {
		p.Auth = m.chatgpt
	}
	return p
}

// ChatGPT 返回账号状态；没有关联登录管理器（例如测试）时返回"未登录"。
func (m *Manager) ChatGPT() ChatGPTStatus {
	m.mu.Lock()
	c, modelsErr := m.chatgpt, m.chatgptModelsErr
	m.mu.Unlock()
	if c == nil {
		return ChatGPTStatus{Status: siwc.Status{State: siwc.StateLoggedOut}}
	}
	st := ChatGPTStatus{Status: c.Status()}
	if st.State == siwc.StateLoggedIn {
		st.ModelsError = modelsErr
	}
	return st
}

// LoginChatGPT 开始登录，返回授权地址（由调用方打开浏览器）。
func (m *Manager) LoginChatGPT(newAccount bool) (string, error) {
	m.mu.Lock()
	c := m.chatgpt
	m.mu.Unlock()
	if c == nil {
		return "", siwc.ErrNotLoggedIn
	}
	return c.StartLogin(newAccount)
}

// CancelChatGPTLogin 取消正在进行的登录。
func (m *Manager) CancelChatGPTLogin() {
	m.mu.Lock()
	c := m.chatgpt
	m.mu.Unlock()
	if c != nil {
		c.CancelLogin()
	}
}

// LogoutChatGPT 退出登录（吊销令牌、删除本地令牌）。
func (m *Manager) LogoutChatGPT(ctx context.Context) error {
	m.mu.Lock()
	c := m.chatgpt
	m.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Logout(ctx)
}

// onChatGPTChange 在登录 / 退出 / 令牌失效后重建厂商目录。
//
// 首次登录（设置文件里还没有 chatgpt 的模型列表）时，从服务器获取模型列表并保存；
// 当前还在用演示厂商时，顺手切换到 chatgpt——与"保存新厂商后自动切换"的行为一致。
func (m *Manager) onChatGPTChange() {
	// 获取模型列表要发网络请求，不能持锁进行。
	m.mu.Lock()
	needModels := m.chatgpt.LoggedIn() && !slices.ContainsFunc(m.file.Providers, func(s ProviderSetting) bool { return s.Name == ChatGPTProfile })
	m.mu.Unlock()
	var models []string
	var modelsErr string
	if needModels {
		models, modelsErr = m.fetchChatGPTModels()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneFile(m.file)
	if len(models) > 0 && !slices.ContainsFunc(next.Providers, func(s ProviderSetting) bool { return s.Name == ChatGPTProfile }) {
		next.Providers = append(next.Providers, ProviderSetting{Name: ChatGPTProfile, Type: "chatgpt", Models: models, DefaultModel: models[0]})
	}
	m.chatgptModelsErr = modelsErr
	if err := m.apply(next); err != nil {
		return // 运行中（ErrBusy）：下一次保存设置或重启时会生效
	}
	if m.engine != nil && m.chatgpt.LoggedIn() && m.currentIsFake() && len(models) > 0 {
		if err := m.engine.SetModel(ChatGPTProfile, models[0]); err == nil {
			next.DefaultProvider, next.DefaultModel = ChatGPTProfile, models[0]
		}
	}
	if err := m.persist(next); err == nil {
		m.file = next
	}
}

func (m *Manager) fetchChatGPTModels() ([]string, string) {
	m.mu.Lock()
	p := m.withAuth(provider.ProfileConfig{Name: ChatGPTProfile, Type: "chatgpt", BaseURL: ChatGPTBaseURL})
	m.mu.Unlock()
	inst, err := m.registry.New(p)
	if err != nil {
		return nil, err.Error()
	}
	lister, ok := inst.(provider.ModelLister)
	if !ok {
		return nil, "不支持获取模型列表"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	models, err := lister.ListModels(ctx)
	if err != nil {
		return nil, "获取模型列表失败：" + err.Error()
	}
	if len(models) == 0 {
		return nil, "服务器返回了空的模型列表"
	}
	return models, ""
}
