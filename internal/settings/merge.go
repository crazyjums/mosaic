package settings

import (
	"maps"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// Source 说明一个配置档来自哪里，界面据此决定能否删除。
type Source string

const (
	SourceConfig   Source = "config"   // 只在 config.toml（或内置默认配置）中
	SourceSettings Source = "settings" // 只在界面设置中
	SourceOverride Source = "override" // 两边都有，界面设置覆盖了配置文件
)

// merge 合并配置文件与设置文件中的配置档，保持顺序：先配置文件的（被覆盖的留在原位），再界面新增的。
//
// 覆盖规则：同名时以设置文件为准；但设置里没填 API Key 时沿用配置文件解析出的密钥
// （例如配置文件写的是 api_key_env，用户在界面上只改了模型列表，不应因此丢掉密钥）。
func merge(base []provider.ProfileConfig, f File) ([]provider.ProfileConfig, map[string]Source) {
	sources := map[string]Source{}
	byName := map[string]ProviderSetting{}
	for _, s := range f.Providers {
		byName[s.Name] = s
	}

	out := make([]provider.ProfileConfig, 0, len(base)+len(f.Providers))
	for _, b := range base {
		s, ok := byName[b.Name]
		if !ok {
			out = append(out, b)
			sources[b.Name] = SourceConfig
			continue
		}
		p := toProfile(s)
		if p.APIKey == "" {
			p.APIKey = b.APIKey
		}
		if p.Options == nil {
			p.Options = maps.Clone(b.Options)
		}
		if p.MaxTokens == 0 {
			p.MaxTokens = b.MaxTokens
		}
		out = append(out, p)
		sources[b.Name] = SourceOverride
		delete(byName, b.Name)
	}
	for _, s := range f.Providers {
		if _, pending := byName[s.Name]; pending {
			out = append(out, toProfile(s))
			sources[s.Name] = SourceSettings
		}
	}
	return out, sources
}

func toProfile(s ProviderSetting) provider.ProfileConfig {
	return provider.ProfileConfig{
		Name:         s.Name,
		Type:         s.Type,
		BaseURL:      s.BaseURL,
		APIKey:       s.APIKey,
		Models:       s.Models,
		DefaultModel: s.DefaultModel,
		Options:      maps.Clone(s.Options),
	}
}
