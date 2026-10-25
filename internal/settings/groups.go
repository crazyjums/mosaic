package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/provider"
	"github.com/crazyjums/myllmapp/internal/usage"
)

// 模型组、代理、用量报表在设置层的部分。逻辑本身在 provider（模型组、代理）和 usage（报表），
// 这里负责：保存到设置文件、校验引用关系、把它们装配进厂商目录、替换目录时保持健康状态。

// GroupInput 是前端提交的模型组表单。
type GroupInput struct {
	// OriginalName 是编辑前的名称；新建时为空。
	OriginalName string `json:"original_name"`
	provider.GroupConfig
}

// GroupView 是发给前端的模型组视图。
type GroupView struct {
	provider.GroupConfig
	// Available 为 false 时 Error 说明原因（例如成员的配置档都被删了）。
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

// groupProfile 把模型组配置转成厂商配置档，让它和普通厂商一起出现在下拉框里。
func groupProfile(g provider.GroupConfig) provider.ProfileConfig {
	return provider.ProfileConfig{
		Name: g.Name, Type: provider.GroupType,
		Models: []string{provider.GroupModel}, DefaultModel: provider.GroupModel,
		Options: provider.GroupOptions(g),
	}
}

// SetUsage 接入用量账本：每次模型调用都会记账；least_used 策略读取今天的用量。
func (m *Manager) SetUsage(rec provider.UsageRecorder, src usage.Source) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recorder, m.usageSrc = rec, src
}

// catalogOptions 返回装配厂商目录用的选项。调用方持有 m.mu。
func (m *Manager) catalogOptions() provider.CatalogOptions {
	if m.health == nil {
		m.health = provider.NewHealth()
	}
	opts := provider.CatalogOptions{Recorder: m.recorder, Health: m.health}
	if src := m.usageSrc; src != nil {
		opts.TodayTokens = (&todayTokens{src: src}).get
	}
	return opts
}

// todayTokens 缓存"今天各配置档用了多少 token"，避免每次请求都查库。
type todayTokens struct {
	src usage.Source
	mu  sync.Mutex
	at  time.Time
	day string
	m   map[string]int64
}

func (t *todayTokens) get(profile string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	day := usage.Day(time.Now())
	if t.m == nil || t.day != day || time.Since(t.at) > 10*time.Second {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if m, err := t.src.ProfileTokens(ctx, day); err == nil {
			t.m, t.day, t.at = m, day, time.Now()
		}
	}
	return t.m[profile]
}

// Groups 返回所有模型组。
func (m *Manager) Groups() []GroupView {
	m.mu.Lock()
	ps, _ := m.merged(m.file)
	groups := slices.Clone(m.file.Groups)
	opts := m.catalogOptions()
	m.mu.Unlock()
	infos := map[string]provider.ProfileInfo{}
	for _, info := range provider.NewCatalogWith(m.registry, ps, opts).Profiles() {
		infos[info.Name] = info
	}
	out := make([]GroupView, 0, len(groups))
	for _, g := range groups {
		info := infos[g.Name]
		out = append(out, GroupView{GroupConfig: g, Available: info.Available, Error: info.Error})
	}
	return out
}

// SaveGroup 新建或修改一个模型组，立即生效。
func (m *Manager) SaveGroup(in GroupInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := in.GroupConfig
	g.Name = strings.TrimSpace(g.Name)
	if g.Strategy == "" {
		g.Strategy = provider.StrategyOrder
	}
	if err := g.Validate(); err != nil {
		return err
	}
	ps, _ := m.merged(m.file)
	byName := map[string]provider.ProfileConfig{}
	for _, p := range ps {
		byName[p.Name] = p
	}
	// 名字不能和任何配置档（厂商或其他模型组）重名，改名时原来的名字除外。
	if g.Name != in.OriginalName {
		if _, dup := byName[g.Name]; dup {
			return fmt.Errorf("名称 %q 已存在", g.Name)
		}
	}
	for _, mem := range g.Members {
		p, ok := byName[mem.Profile]
		switch {
		case !ok:
			return fmt.Errorf("成员 %q：配置档不存在", mem.Profile)
		case p.Type == provider.GroupType:
			return fmt.Errorf("成员 %q 是模型组，模型组不能嵌套", mem.Profile)
		case !slices.Contains(p.Models, mem.Model):
			return fmt.Errorf("成员 %s：模型 %q 不在这个配置档的模型列表里", mem.Profile, mem.Model)
		}
	}

	next := cloneFile(m.file)
	next.Groups = slices.DeleteFunc(next.Groups, func(x provider.GroupConfig) bool {
		return x.Name == g.Name || (in.OriginalName != "" && x.Name == in.OriginalName)
	})
	next.Groups = append(next.Groups, g)
	if in.OriginalName != "" && in.OriginalName != g.Name && next.DefaultProvider == in.OriginalName {
		next.DefaultProvider = g.Name
	}
	editingCurrent := false
	if m.engine != nil {
		editingCurrent = m.engine.Status().Provider == in.OriginalName && in.OriginalName != ""
	}
	if err := m.apply(next); err != nil {
		return err
	}
	if m.engine != nil && editingCurrent && in.OriginalName != g.Name {
		_ = m.engine.SetModel(g.Name, provider.GroupModel) // 改名后跟随新名字
	}
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

// DeleteGroup 删除模型组。正在使用它的会话会回落到默认的厂商。
func (m *Manager) DeleteGroup(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.ContainsFunc(m.file.Groups, func(g provider.GroupConfig) bool { return g.Name == name }) {
		return fmt.Errorf("模型组 %q 不存在", name)
	}
	next := cloneFile(m.file)
	next.Groups = slices.DeleteFunc(next.Groups, func(g provider.GroupConfig) bool { return g.Name == name })
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

// GroupStatuses 返回各模型组成员的运行状态（健康 / 冷却中 / 已停用）。
func (m *Manager) GroupStatuses() []provider.GroupStatus {
	m.mu.Lock()
	e := m.engine
	m.mu.Unlock()
	if e == nil {
		return nil
	}
	return e.GroupStatuses()
}

// ResetMember 手动恢复一个成员（界面上的"恢复"按钮）：清掉冷却和停用状态，下一个请求会重新尝试它。
func (m *Manager) ResetMember(profile, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.health != nil {
		m.health.Reset(provider.MemberKey(profile, model))
	}
}

// ---- 网络代理 ----

// ProxyView 是全局默认代理的视图。地址里的密码已经脱敏。
type ProxyView struct {
	Set     bool   `json:"set"`
	Display string `json:"display"`
}

// Proxy 返回全局默认代理。
func (m *Manager) Proxy() ProxyView {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ProxyView{Set: m.file.Proxy != "", Display: provider.RedactProxy(m.file.Proxy)}
}

// SetProxy 保存全局默认代理，立即对所有没有单独设置代理的厂商生效。空串表示不设置。
// 前端拿到的是脱敏后的地址；原样提交脱敏的地址表示"不修改"，以免把密码覆盖成 ***。
func (m *Manager) SetProxy(raw string) error {
	raw = strings.TrimSpace(raw)
	if err := provider.ValidateProxy(raw); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if raw != "" && raw == provider.RedactProxy(m.file.Proxy) {
		return nil
	}
	next := cloneFile(m.file)
	next.Proxy = raw
	if err := m.apply(next); err != nil {
		return err
	}
	if err := m.persist(next); err != nil {
		return err
	}
	m.file = next
	return nil
}

// applyProxy 给没有单独设置代理的配置档填上全局默认代理。
func (m *Manager) applyProxy(p provider.ProfileConfig) provider.ProfileConfig {
	if m.file.Proxy == "" || p.Type == provider.GroupType {
		return p
	}
	if cur, _ := p.Options["proxy"].(string); strings.TrimSpace(cur) != "" {
		return p
	}
	opts := make(map[string]any, len(p.Options)+1)
	for k, v := range p.Options {
		opts[k] = v
	}
	opts["proxy"] = m.file.Proxy
	p.Options = opts
	return p
}

// redactOptions 返回去掉代理密码的选项副本，用于发给前端。
func redactOptions(o map[string]any) map[string]any {
	if o == nil {
		return nil
	}
	out := make(map[string]any, len(o))
	for k, v := range o {
		out[k] = v
	}
	if s, ok := out["proxy"].(string); ok {
		out["proxy"] = provider.RedactProxy(s)
	}
	return out
}

// restoreProxySecret：前端提交的是脱敏后的代理地址（用户没改它）时，换回已保存的完整地址。
func (m *Manager) restoreProxySecret(in *ProviderInput) {
	cur, _ := in.Options["proxy"].(string)
	if cur == "" {
		return
	}
	ps, _ := m.merged(m.file)
	for _, name := range []string{in.OriginalName, in.Name} {
		for _, p := range ps {
			if p.Name != name {
				continue
			}
			if old, _ := p.Options["proxy"].(string); old != "" && cur == provider.RedactProxy(old) {
				in.Options["proxy"] = old
				return
			}
		}
	}
}

// ---- 用量报表 ----

// UsageRequest 是前端的报表请求。
type UsageRequest struct {
	// Range：today / 7d / 30d / all / custom（custom 用 From、To，格式 YYYY-MM-DD）。
	Range string `json:"range"`
	From  string `json:"from"`
	To    string `json:"to"`
	// By：day / session / model / profile。
	By      string `json:"by"`
	Profile string `json:"profile"`
	Model   string `json:"model"`
	Group   string `json:"group"`
}

// UsageReportView 是发给前端的用量报表。花费一律是美元，换算人民币由界面按用户填的汇率做。
type UsageReportView struct {
	usage.Report
	From string `json:"from"`
	To   string `json:"to"`
	// Profiles 是账本里出现过的配置档，供筛选下拉框使用。
	Profiles []string `json:"profiles"`
}

func (r UsageRequest) query(now time.Time) (usage.Query, error) {
	q := usage.Query{By: r.By, Profile: r.Profile, Model: r.Model, Group: r.Group}
	switch q.By {
	case "", usage.ByDay:
		q.By = usage.ByDay
	case usage.BySession, usage.ByModel, usage.ByProfile:
	default:
		return q, fmt.Errorf("未知的分组方式 %q", r.By)
	}
	day := func(back int) string { return usage.Day(now.AddDate(0, 0, -back)) }
	switch r.Range {
	case "", "today":
		q.From, q.To = day(0), day(0)
	case "7d":
		q.From, q.To = day(6), day(0)
	case "30d":
		q.From, q.To = day(29), day(0)
	case "all":
	case "custom":
		q.From, q.To = r.From, r.To
		for _, d := range []string{q.From, q.To} {
			if d == "" {
				continue
			}
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return q, fmt.Errorf("日期 %q 格式应为 YYYY-MM-DD", d)
			}
		}
		if q.From != "" && q.To != "" && q.From > q.To {
			return q, fmt.Errorf("开始日期晚于结束日期")
		}
	default:
		return q, fmt.Errorf("未知的时间范围 %q", r.Range)
	}
	return q, nil
}

// priceFunc 按配置档里的 models_meta 查价格；套餐类厂商（ChatGPT 账号登录）不计金额。
// 缓存价没填时按普通输入价计算：估算偏高比偏低更安全。
func (m *Manager) priceFunc() usage.PriceFunc {
	m.mu.Lock()
	ps, _ := m.merged(m.file)
	m.mu.Unlock()
	type key struct{ p, m string }
	prices := map[key]usage.PriceInfo{}
	plan := map[string]bool{}
	for _, p := range ps {
		if p.Type == "chatgpt" {
			plan[p.Name] = true
			continue
		}
		for model, meta := range provider.ParseModelMeta(p.Options) {
			if meta.InputPrice <= 0 && meta.OutputPrice <= 0 {
				continue
			}
			cr, cw := meta.CacheReadPrice, meta.CacheWritePrice
			if cr <= 0 {
				cr = meta.InputPrice
			}
			if cw <= 0 {
				cw = meta.InputPrice
			}
			prices[key{p.Name, model}] = usage.PriceInfo{Known: true, Price: usage.Price{In: meta.InputPrice, Out: meta.OutputPrice, CacheRead: cr, CacheWrite: cw}}
		}
	}
	return func(profile, model string) usage.PriceInfo {
		if plan[profile] {
			return usage.PriceInfo{Plan: true}
		}
		return prices[key{profile, model}]
	}
}

// UsageReport 生成用量报表。
func (m *Manager) UsageReport(ctx context.Context, req UsageRequest) (UsageReportView, error) {
	m.mu.Lock()
	src := m.usageSrc
	m.mu.Unlock()
	if src == nil {
		return UsageReportView{}, fmt.Errorf("用量统计不可用")
	}
	q, err := req.query(time.Now())
	if err != nil {
		return UsageReportView{}, err
	}
	rows, err := src.UsageRows(ctx, q)
	if err != nil {
		return UsageReportView{}, err
	}
	view := UsageReportView{Report: usage.Build(rows, q.By, m.priceFunc()), From: q.From, To: q.To, Profiles: []string{}}
	// 筛选下拉框要列出范围内所有配置档，所以用不带 profile/model 筛选的查询。
	all := q
	all.Profile, all.Model, all.By = "", "", usage.ByProfile
	if prows, err := src.UsageRows(ctx, all); err == nil {
		seen := map[string]bool{}
		for _, r := range prows {
			if !seen[r.Profile] {
				seen[r.Profile] = true
				view.Profiles = append(view.Profiles, r.Profile)
			}
		}
		slices.Sort(view.Profiles)
	}
	return view, nil
}

// ExportUsageCSV 导出范围内的原始记录。
func (m *Manager) ExportUsageCSV(ctx context.Context, req UsageRequest) (string, error) {
	m.mu.Lock()
	src := m.usageSrc
	m.mu.Unlock()
	if src == nil {
		return "", fmt.Errorf("用量统计不可用")
	}
	q, err := req.query(time.Now())
	if err != nil {
		return "", err
	}
	events, err := src.UsageEvents(ctx, q)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := usage.WriteCSV(&b, events); err != nil {
		return "", err
	}
	return b.String(), nil
}
