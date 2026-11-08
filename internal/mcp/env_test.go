package mcp

import (
	"strings"
	"testing"
)

func TestToolchainEnvPassthrough(t *testing.T) {
	pass := []string{
		"NODE_EXTRA_CA_CERTS", "npm_config_registry", "NPM_CONFIG_PROXY", "npm_config_https_proxy", "npm_config_cafile",
		"UV_INDEX_URL", "UV_NATIVE_TLS", "PIP_INDEX_URL", "CARGO_HOME", "GOPROXY", "GOFLAGS", "REQUESTS_CA_BUNDLE",
	}
	for _, n := range pass {
		if !isToolchainEnv(n) {
			t.Errorf("%s 应当透传", n)
		}
	}
	block := []string{
		// 凭据：即使在放行的前缀下也要剔除
		"npm_config__authToken", "NPM_CONFIG__AUTH", "npm_config_//registry.npmjs.org/:_authToken", "UV_INDEX_PASSWORD", "PIP_INDEX_URL_PASSWORD",
		"CARGO_REGISTRY_TOKEN", "UV_PUBLISH_TOKEN",
		// 能改变加载什么代码的变量
		"NODE_OPTIONS", "PYTHONPATH", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "NODE_PATH",
		// 别的密钥
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GH_TOKEN",
	}
	for _, n := range block {
		if isToolchainEnv(n) {
			t.Errorf("%s 不应透传", n)
		}
	}
}

func TestBaseEnvIncludesToolchainButNotSecrets(t *testing.T) {
	t.Setenv("NODE_EXTRA_CA_CERTS", "/ca.pem")
	t.Setenv("npm_config_registry", "https://registry.npmmirror.com")
	t.Setenv("npm_config__authToken", "s3cret")
	t.Setenv("OPENAI_API_KEY", "sk-xxx")
	got := map[string]string{}
	for _, kv := range BaseEnv() {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if got["NODE_EXTRA_CA_CERTS"] != "/ca.pem" || got["npm_config_registry"] == "" {
		t.Fatalf("工具链变量应透传: %v", got)
	}
	if _, ok := got["npm_config__authToken"]; ok {
		t.Fatal("npm 的认证令牌不能透传")
	}
	if _, ok := got["OPENAI_API_KEY"]; ok {
		t.Fatal("厂商密钥不能透传")
	}
}
