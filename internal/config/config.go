package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// Config 是完整配置。字段与 config.example.toml 一一对应，那里有每个字段的说明。
type Config struct {
	DefaultProvider string `toml:"default_provider"`
	DefaultModel    string `toml:"default_model"`

	// Workspace 是工具的工作区根目录，相对路径相对于当前工作目录。
	Workspace string `toml:"workspace"`
	// DataDir 存放会话数据库等本地数据，默认 ~/.myllmapp。
	DataDir string `toml:"data_dir"`

	PermissionMode      string `toml:"permission_mode"` // readonly / default / accept_edits / auto
	MaxSteps            int    `toml:"max_steps"`
	MaxTokens           int    `toml:"max_tokens"`
	ShellTimeoutSeconds int    `toml:"shell_timeout_seconds"`
	SystemPrompt        string `toml:"system_prompt"`

	Providers []ProviderConfig `toml:"providers"`

	// Source 是实际加载的配置文件路径，为空表示使用了内置默认配置。
	Source string `toml:"-"`
}

// ProviderConfig 是配置文件里的一个厂商配置档。
type ProviderConfig struct {
	Name         string         `toml:"name"`
	Type         string         `toml:"type"`
	BaseURL      string         `toml:"base_url"`
	APIKey       string         `toml:"api_key"`
	APIKeyEnv    string         `toml:"api_key_env"`
	Models       []string       `toml:"models"`
	DefaultModel string         `toml:"default_model"`
	MaxTokens    int            `toml:"max_tokens"`
	Options      map[string]any `toml:"options"`
}

// EnvConfigPath 是指定配置文件路径的环境变量。
const EnvConfigPath = "MYLLMAPP_CONFIG"

// Default 返回内置默认配置：没有配置文件也能直接用 fake 跑起来，
// 设置了常见环境变量（OPENAI_API_KEY 等）的厂商也立即可用。
func Default() *Config {
	return &Config{
		DefaultProvider: "fake",
		Workspace:       ".",
		PermissionMode:  "default",
		Providers: []ProviderConfig{
			{Name: "fake", Type: "fake", Models: []string{"demo", "echo"}, DefaultModel: "demo",
				// 给演示模型也配上上下文窗口和（假想的）单价，这样不配置真实厂商也能看到状态栏的全部效果。
				Options: map[string]any{"scenario": "demo", "delay_ms": int64(25), "models_meta": map[string]any{
					"demo": map[string]any{"context_window": 200000, "input_price": 3.0, "output_price": 15.0},
				}}},
			{Name: "anthropic", Type: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY",
				Models: []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}, DefaultModel: "claude-opus-5-5",
				// 上下文窗口与单价（美元 / 百万 token）只用于状态栏显示，价格以官网为准。
				Options: map[string]any{"models_meta": map[string]any{
					"claude-opus-5-5":   map[string]any{"context_window": 1000000, "input_price": 4.0, "output_price": 20.0},
					"claude-sonnet-5-5": map[string]any{"context_window": 1000000, "input_price": 2.0, "output_price": 10.0},
					"claude-haiku-5-5":  map[string]any{"context_window": 1000000, "input_price": 0.1, "output_price": 0.5},
				}}},
			{Name: "openai", Type: "openai", APIKeyEnv: "OPENAI_API_KEY", BaseURL: "https://api.openai.com/v1",
				Models: []string{"gpt-4o-mini"}},
			{Name: "deepseek", Type: "openai", APIKeyEnv: "DEEPSEEK_API_KEY", BaseURL: "https://api.deepseek.com/v1",
				Models: []string{"deepseek-chat", "deepseek-reasoner"}},
		},
	}
}

// Load 加载配置。explicit 为命令行指定的路径，可以为空。
//
// 指定了路径但文件不存在时报错（用户显式要求的东西找不到，应该告诉他）；
// 按默认位置查找时找不到则静默回落到内置默认配置。
func Load(explicit string) (*Config, error) {
	path, err := findConfig(explicit)
	if err != nil {
		return nil, err
	}
	if path == "" {
		cfg := Default()
		return cfg, cfg.validate()
	}
	cfg := &Config{}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.Source = path
	cfg.applyDefaults()
	return cfg, cfg.validate()
}

func findConfig(explicit string) (string, error) {
	if explicit == "" {
		explicit = os.Getenv(EnvConfigPath)
	}
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config: %w", err)
		}
		return explicit, nil
	}
	candidates := []string{"config.toml"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".myllmapp", "config.toml"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", nil
}

func (c *Config) applyDefaults() {
	if c.Workspace == "" {
		c.Workspace = "."
	}
	if c.PermissionMode == "" {
		c.PermissionMode = "default"
	}
	if len(c.Providers) == 0 {
		c.Providers = Default().Providers
	}
}

func (c *Config) validate() error {
	seen := map[string]bool{}
	for i, p := range c.Providers {
		if p.Name == "" || p.Type == "" {
			return fmt.Errorf("config: providers[%d] needs both name and type", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("config: duplicate provider name %q", p.Name)
		}
		seen[p.Name] = true
	}
	if _, err := permission.ParseMode(c.PermissionMode); err != nil {
		return fmt.Errorf("config: permission_mode: %w", err)
	}
	return nil
}

// ResolveDataDir 返回数据目录的绝对路径。
func (c *Config) ResolveDataDir() (string, error) {
	if c.DataDir != "" {
		return filepath.Abs(c.DataDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("config: cannot determine home directory, please set data_dir")
	}
	return filepath.Join(home, ".myllmapp"), nil
}

// Profiles 把配置档转换成 provider.ProfileConfig，并解析密钥。
//
// 密钥优先级：api_key_env 指向的环境变量（非空时）> 配置文件中的 api_key。
// 环境变量优先，是为了让"配置文件放在仓库里共享、密钥各自放在环境里"成为最自然的用法。
func (c *Config) Profiles() []provider.ProfileConfig {
	out := make([]provider.ProfileConfig, 0, len(c.Providers))
	for _, p := range c.Providers {
		key := p.APIKey
		if p.APIKeyEnv != "" {
			if v := os.Getenv(p.APIKeyEnv); v != "" {
				key = v
			}
		}
		maxTokens := p.MaxTokens
		if maxTokens == 0 {
			maxTokens = c.MaxTokens
		}
		out = append(out, provider.ProfileConfig{
			Name:         p.Name,
			Type:         p.Type,
			BaseURL:      p.BaseURL,
			APIKey:       key,
			Models:       p.Models,
			DefaultModel: p.DefaultModel,
			MaxTokens:    maxTokens,
			Options:      p.Options,
		})
	}
	return out
}
