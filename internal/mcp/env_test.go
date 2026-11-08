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

func TestToolchainEnvDropsEmbeddedCredentialsInValues(t *testing.T) {
	t.Setenv("PIP_INDEX_URL", "https://user:private-token@packages.example/simple")
	t.Setenv("npm_config_registry", "https://__token__@registry.example/")
	t.Setenv("UV_INDEX_URL", "https://pypi.example/simple") // 没有凭据：正常传
	t.Setenv("GOPROXY", "https://goproxy.example,direct")
	got := map[string]string{}
	for _, kv := range BaseEnv() {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for _, n := range []string{"PIP_INDEX_URL", "npm_config_registry"} {
		if _, ok := got[n]; ok {
			t.Errorf("%s 的值里有内嵌凭据，不应传给插件: %q", n, got[n])
		}
	}
	for _, n := range []string{"UV_INDEX_URL", "GOPROXY"} {
		if got[n] == "" {
			t.Errorf("%s 没有凭据，应该传", n)
		}
	}
	for v, want := range map[string]bool{
		"https://user:pw@host/x": true, "http://tok@host": true, "HTTPS://U:P@HOST": true, "git+ssh://git@github.com/x": true,
		"https://host/path@2x": false, "https://host/?a=b@c": false, "/usr/local/ca.pem": false, "https://pypi.example/simple": false,
	} {
		if hasEmbeddedCredential(v) != want {
			t.Errorf("hasEmbeddedCredential(%q) = %v, want %v", v, !want, want)
		}
	}
}
