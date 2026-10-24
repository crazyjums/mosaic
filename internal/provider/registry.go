package provider

import (
	"fmt"
	"sort"
	"sync"
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
}

// Catalog 是"当前可用的所有厂商实例"，按配置档名称索引。
// core.Engine 通过它切换厂商；它本身不关心配置从哪里来。
type Catalog struct {
	order     []string
	profiles  map[string]ProfileConfig
	instances map[string]Provider
	errors    map[string]error
}

// NewCatalog 为每个配置档创建实例。单个配置档失败不影响其他配置档，
// 失败原因记录下来展示给用户——"DeepSeek 没配密钥"不应该导致整个程序起不来。
func NewCatalog(reg *Registry, cfgs []ProfileConfig) *Catalog {
	c := &Catalog{
		profiles:  map[string]ProfileConfig{},
		instances: map[string]Provider{},
		errors:    map[string]error{},
	}
	for _, cfg := range cfgs {
		if _, dup := c.profiles[cfg.Name]; dup {
			continue
		}
		c.order = append(c.order, cfg.Name)
		c.profiles[cfg.Name] = cfg
		p, err := reg.New(cfg)
		if err != nil {
			c.errors[cfg.Name] = err
			continue
		}
		c.instances[cfg.Name] = p
	}
	return c
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
		}
		if err := c.errors[name]; err != nil {
			info.Error = err.Error()
		}
		out = append(out, info)
	}
	return out
}
