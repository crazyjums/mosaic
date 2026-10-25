package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileAndEnvKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(path, []byte(`
default_provider = "ds"
max_steps = 7

[[providers]]
name = "ds"
type = "openai"
base_url = "https://api.deepseek.com/v1"
api_key = "from-file"
api_key_env = "TEST_DS_KEY"
models = ["deepseek-chat"]

[providers.options]
max_tokens_field = "max_tokens"
`), 0o644)

	t.Setenv("TEST_DS_KEY", "from-env")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != path || cfg.MaxSteps != 7 || cfg.PermissionMode != "default" || cfg.Workspace != "." {
		t.Fatalf("cfg: %+v", cfg)
	}
	ps := cfg.Profiles()
	if len(ps) != 1 || ps[0].APIKey != "from-env" || ps[0].Options["max_tokens_field"] != "max_tokens" {
		t.Fatalf("环境变量应优先于文件中的密钥: %+v", ps)
	}

	t.Setenv("TEST_DS_KEY", "")
	if cfg.Profiles()[0].APIKey != "from-file" {
		t.Fatal("环境变量为空时应回落到文件中的密钥")
	}
}

func TestLoadDefaultsWhenNoFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvConfigPath, "")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "" || cfg.DefaultProvider != "" || len(cfg.Providers) < 2 {
		t.Fatalf("应使用内置默认配置: %+v", cfg)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load("/definitely/not/here.toml"); err == nil {
		t.Fatal("显式指定的文件不存在应报错")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.toml")
	os.WriteFile(bad, []byte("[[providers]]\nname = \"x\"\n"), 0o644)
	if _, err := Load(bad); err == nil {
		t.Fatal("缺少 type 应报错")
	}
	mode := filepath.Join(dir, "mode.toml")
	os.WriteFile(mode, []byte("permission_mode = \"yolo\"\n"), 0o644)
	if _, err := Load(mode); err == nil {
		t.Fatal("非法 permission_mode 应报错")
	}
}

// 内置默认配置不带演示厂商（全新安装时界面直接引导配置真实模型）；EnableDemo 给测试和演示用。
func TestDefaultHasNoDemoUntilEnabled(t *testing.T) {
	cfg := Default()
	for _, p := range cfg.Providers {
		if p.Type == "fake" {
			t.Fatalf("内置默认配置不应包含演示厂商: %s", p.Name)
		}
	}
	cfg.EnableDemo()
	if cfg.DefaultProvider != "fake" || cfg.Providers[0].Type != "fake" {
		t.Fatalf("EnableDemo 应把 fake 放最前并设为默认: %+v", cfg.Providers[0])
	}
}
