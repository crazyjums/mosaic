package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ProfileConfig 是一个"厂商配置档"：同一个适配器类型可以有多个配置档，
// 例如 type=openai 的 "openai"、"deepseek"、"ollama" 三个配置档，只是 BaseURL 和密钥不同。
//
// 这层区分（适配器类型 vs 配置档）是 OpenAI 兼容协议能"覆盖大部分厂商"的关键：
// 新增一个兼容厂商只需要写配置，不需要写代码。
type ProfileConfig struct {
	Name         string
	Type         string // 适配器类型，对应 Register 的 key
	BaseURL      string
	APIKey       string // 已从环境变量或配置文件解析出的明文密钥，只在内存中
	Models       []string
	DefaultModel string
	MaxTokens    int
	// Options 是适配器特有的选项（例如 fake 的脚本路径、anthropic 的 effort），
	// 由各适配器自己解释，避免为每个厂商的特殊参数修改这个公共结构。
	Options map[string]any
	// Auth 是动态令牌来源，用于 OAuth 登录这类"令牌会过期、需要刷新"的认证方式
	// （例如用 ChatGPT 账号登录）。为 nil 时使用 APIKey。
	Auth TokenSource
}

// TokenSource 提供访问令牌。实现负责缓存和刷新，适配器每次请求前调用一次。
//
// 为什么不在登录后把令牌写进 APIKey：OAuth 访问令牌一小时就过期，
// 而厂商目录（Catalog）只在配置变化时重建；交给 TokenSource 按需刷新，适配器就不用关心过期。
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Factory 根据配置档创建 Provider 实例。
type Factory func(cfg ProfileConfig) (Provider, error)

// Registry 保存"适配器类型 → 工厂"的映射。
//
// 用注册表而不是在某处写 switch-case：新增厂商时只需在新包里 Register，
// 不需要修改任何已有代码（开闭原则）。这与 database/sql 注册驱动的方式相同。
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }

// DefaultRegistry 是全局注册表，各厂商子包在 init() 中向它注册。
//
// 取舍：全局状态 + init() 注册 vs. 显式在 main 里逐个注册。
// 前者让"新增厂商 = 新包 + 一行空导入"，代价是注册时机隐式；
// 为了让隐式不至于难找，所有空导入集中在 provider/all 一个文件里。测试可以用 NewRegistry 隔离。
var DefaultRegistry = NewRegistry()

// Register 向默认注册表注册适配器。重复注册会 panic，因为这几乎一定是编程错误。
func Register(adapterType string, f Factory) { DefaultRegistry.Register(adapterType, f) }

func (r *Registry) Register(adapterType string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[adapterType]; dup {
		panic("provider: duplicate adapter type " + adapterType)
	}
	r.factories[adapterType] = f
}

// Adapters 返回已注册的适配器类型（排序后），用于错误提示和界面展示。
func (r *Registry) Adapters() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// New 用配置档创建 Provider。
func (r *Registry) New(cfg ProfileConfig) (Provider, error) {
	r.mu.RLock()
	f, ok := r.factories[cfg.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider: unknown adapter type %q (registered: %v)", cfg.Type, r.Adapters())
	}
	return f(cfg)
}

// ProfileInfo 是配置档的公开视图，可以安全地发给前端（不含密钥）。
type ProfileInfo struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model"`
	HasAPIKey    bool     `json:"has_api_key"`
	// Available 为 false 时 Error 说明原因（例如缺少密钥），界面应置灰该选项。
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`

	// ModelMeta 是每个模型的附加信息（上下文窗口、单价），来自配置档 options 中的 models_meta，
	// 用于状态栏显示上下文占用和预估花费。没有配置的模型不在这个表里，界面就不显示比例和花费。
	ModelMeta map[string]ModelMeta `json:"model_meta"`
}

// ModelMeta 是单个模型的附加信息。字段为 0 表示未配置。
//
// 为什么按模型而不是按厂商：同一个厂商的模型差别很大（同一家的大小模型单价能差几十倍，
// 上下文窗口也不同），按厂商只填一个数会让花费估算严重失真。
type ModelMeta struct {
	ContextWindow int `json:"context_window,omitempty"`
	// 单价的单位是"美元 / 百万 token"。
	InputPrice  float64 `json:"input_price,omitempty"`
	OutputPrice float64 `json:"output_price,omitempty"`
	// 缓存读 / 写的单价。没填时按普通输入价计算（估算会偏高而不是偏低）。
	CacheReadPrice  float64 `json:"cache_read_price,omitempty"`
	CacheWritePrice float64 `json:"cache_write_price,omitempty"`
}

// ParseModelMeta 从 options["models_meta"] 解析每个模型的附加信息。
// 结构：{"模型名": {"context_window": 200000, "input_price": 3, "output_price": 15}}。
// TOML 和 JSON 解析出的数字类型不同（int64 / float64），都要支持。
func ParseModelMeta(opts map[string]any) map[string]ModelMeta {
	raw, _ := opts["models_meta"].(map[string]any)
	out := map[string]ModelMeta{}
	for model, v := range raw {
		m, _ := v.(map[string]any)
		meta := ModelMeta{
			ContextWindow:   int(number(m["context_window"])),
			InputPrice:      number(m["input_price"]),
			OutputPrice:     number(m["output_price"]),
			CacheReadPrice:  number(m["cache_read_price"]),
			CacheWritePrice: number(m["cache_write_price"]),
		}
		if meta != (ModelMeta{}) {
			out[model] = meta
		}
	}
	return out
}

func number(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

// Catalog 是"当前可用的所有厂商实例"，按配置档名称索引。
// core.Engine 通过它切换厂商；它本身不关心配置从哪里来。
type Catalog struct {
	order     []string
	profiles  map[string]ProfileConfig
	instances map[string]Provider
	groups    map[string]*Group
	errors    map[string]error
	health    *Health
}

// CatalogOptions 是装配目录时可选的横切能力。
type CatalogOptions struct {
	// Recorder 非空时，每次模型调用都会记一条用量（见 usage.go）。
	Recorder UsageRecorder
	// Health 保存模型组成员的健康状态。厂商目录在每次保存设置时重建，状态要在重建之间保持，所以由调用方持有。
	Health *Health
	// TodayTokens 返回某个配置档今天用掉的 token，供模型组的 least_used 策略使用。可以为 nil。
	TodayTokens func(profile string) int64
}

// NewCatalog 为每个配置档创建实例。单个配置档失败不影响其他配置档，
// 失败原因记录下来展示给用户——"DeepSeek 没配密钥"不应该导致整个程序起不来。
func NewCatalog(reg *Registry, cfgs []ProfileConfig) *Catalog {
	return NewCatalogWith(reg, cfgs, CatalogOptions{})
}

// NewCatalogWith 同 NewCatalog，并装配用量计量和模型组。
func NewCatalogWith(reg *Registry, cfgs []ProfileConfig, opts CatalogOptions) *Catalog {
	c := &Catalog{
		profiles:  map[string]ProfileConfig{},
		instances: map[string]Provider{},
		groups:    map[string]*Group{},
		errors:    map[string]error{},
		health:    opts.Health,
	}
	if c.health == nil {
		c.health = NewHealth()
	}
	// 原始实例（没有计量包装）：模型组自己给每次尝试记账，才能标出哪些是"已转交"。
	raw := map[string]Provider{}
	var groupCfgs []ProfileConfig
	for _, cfg := range cfgs {
		if _, dup := c.profiles[cfg.Name]; dup {
			continue
		}
		c.order = append(c.order, cfg.Name)
		c.profiles[cfg.Name] = cfg
		if cfg.Type == GroupType {
			groupCfgs = append(groupCfgs, cfg)
			continue
		}
		p, err := reg.New(cfg)
		if err != nil {
			c.errors[cfg.Name] = err
			continue
		}
		raw[cfg.Name] = p
		if opts.Recorder != nil {
			p = &metered{inner: p, rec: opts.Recorder, profile: cfg.Name, cacheSeparate: cacheSeparateType(cfg.Type)}
		}
		c.instances[cfg.Name] = p
	}
	for _, cfg := range groupCfgs {
		g, err := c.buildGroup(cfg, raw, opts)
		if err != nil {
			c.errors[cfg.Name] = err
			continue
		}
		c.groups[cfg.Name] = g
		c.instances[cfg.Name] = g
	}
	return c
}

// ParseGroup 从配置档的 options 解析模型组配置。
func ParseGroup(cfg ProfileConfig) (GroupConfig, error) {
	gc := GroupConfig{Sticky: true, FirstByteTimeoutS: DefaultFirstByteTimeoutS}
	b, err := json.Marshal(cfg.Options)
	if err != nil {
		return gc, err
	}
	if err := json.Unmarshal(b, &gc); err != nil {
		return gc, fmt.Errorf("模型组配置格式不正确: %w", err)
	}
	gc.Name = cfg.Name
	if gc.Strategy == "" {
		gc.Strategy = StrategyOrder
	}
	return gc, gc.Validate()
}

// GroupOptions 把模型组配置转成配置档的 options（保存进设置文件的形式）。
func GroupOptions(gc GroupConfig) map[string]any {
	return map[string]any{
		"members":              gc.Members,
		"strategy":             gc.Strategy,
		"sticky":               gc.Sticky,
		"first_byte_timeout_s": gc.FirstByteTimeoutS,
	}
}

func (c *Catalog) buildGroup(cfg ProfileConfig, raw map[string]Provider, opts CatalogOptions) (*Group, error) {
	gc, err := ParseGroup(cfg)
	if err != nil {
		return nil, err
	}
	g := &Group{cfg: gc, health: c.health, rec: opts.Recorder, todayTokens: opts.TodayTokens, firstByte: time.Duration(gc.FirstByteTimeoutS) * time.Second}
	usable := 0
	for _, m := range gc.Members {
		gm := &groupMember{GroupMember: m, key: MemberKey(m.Profile, m.Model)}
		switch mc, ok := c.profiles[m.Profile]; {
		case !ok:
			gm.unavailable = "配置档不存在（可能被删除了）"
		case mc.Type == GroupType:
			gm.unavailable = "模型组不能嵌套"
		case raw[m.Profile] == nil:
			if e := c.errors[m.Profile]; e != nil {
				gm.unavailable = e.Error()
			} else {
				gm.unavailable = "配置档不可用"
			}
		default:
			gm.leaf = raw[m.Profile]
			gm.cacheSeparate = cacheSeparateType(mc.Type)
			gm.ctxWindow = ParseModelMeta(mc.Options)[m.Model].ContextWindow
			usable++
		}
		g.members = append(g.members, gm)
	}
	if usable == 0 {
		return nil, errors.New("模型组里没有可用的成员（引用的配置档不存在或缺少密钥）")
	}
	return g, nil
}

// Health 返回健康状态表。
func (c *Catalog) Health() *Health { return c.health }

// GroupStatuses 返回所有模型组的运行状态（包括因配置错误而不可用的组，此时 Members 为空）。
func (c *Catalog) GroupStatuses() []GroupStatus {
	var out []GroupStatus
	for _, name := range c.order {
		if c.profiles[name].Type != GroupType {
			continue
		}
		if g := c.groups[name]; g != nil {
			out = append(out, g.Status())
		} else {
			out = append(out, GroupStatus{Name: name})
		}
	}
	return out
}

// Get 返回配置档对应的 Provider 和配置。
func (c *Catalog) Get(name string) (Provider, ProfileConfig, error) {
	cfg, ok := c.profiles[name]
	if !ok {
		return nil, ProfileConfig{}, fmt.Errorf("provider: profile %q not found", name)
	}
	if err := c.errors[name]; err != nil {
		return nil, cfg, fmt.Errorf("provider: profile %q unavailable: %w", name, err)
	}
	return c.instances[name], cfg, nil
}

// Profiles 按配置顺序返回所有配置档的公开信息。
func (c *Catalog) Profiles() []ProfileInfo {
	out := make([]ProfileInfo, 0, len(c.order))
	for _, name := range c.order {
		cfg := c.profiles[name]
		info := ProfileInfo{
			Name:         cfg.Name,
			Type:         cfg.Type,
			Models:       cfg.Models,
			DefaultModel: cfg.DefaultModel,
			HasAPIKey:    cfg.APIKey != "",
			Available:    c.errors[name] == nil,
			ModelMeta:    ParseModelMeta(cfg.Options),
		}
		if err := c.errors[name]; err != nil {
			info.Error = err.Error()
		}
		out = append(out, info)
	}
	return out
}
