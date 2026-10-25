package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/crazyjums/myllmapp/internal/config"
)

const root = "../.."

// skipDir 返回 true 的目录不检查（依赖、构建产物、版本库元数据）。
func skipDir(path string) bool {
	base := filepath.Base(path)
	return base == "node_modules" || base == ".git" || base == "dist" || base == "wailsjs" || base == "bin"
}

func TestEveryPackageHasDocGo(t *testing.T) {
	dirs := map[string]bool{}
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skipDir(path) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			dirs[filepath.Dir(path)] = true
		}
		return nil
	})
	for dir := range dirs {
		if filepath.Clean(dir) == filepath.Clean(root) {
			continue // 仓库根目录的 main.go 是 Wails 入口，文件头已有说明
		}
		if _, err := os.Stat(filepath.Join(dir, "doc.go")); err != nil {
			t.Errorf("%s 缺少 doc.go", dir)
		}
	}
}

// 常见厂商密钥的形态。命中即失败——宁可误报，也不要把密钥推上去。
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`),     // Anthropic
	regexp.MustCompile(`sk-(proj-)?[A-Za-z0-9]{32,}`),    // OpenAI / DeepSeek 等
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),               // AWS
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),            // GitHub
	regexp.MustCompile(`(?i)api_key\s*=\s*"[^"$]{16,}"`), // 配置文件里写死的长密钥
}

func TestNoSecretsInRepo(t *testing.T) {
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "package-lock.json") || strings.HasSuffix(path, "go.sum") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || len(b) > 2<<20 {
			return nil
		}
		for _, re := range secretPatterns {
			if loc := re.FindIndex(b); loc != nil {
				t.Errorf("%s 中疑似包含密钥: %q", path, string(b[loc[0]:min(loc[1], loc[0]+16)])+"…")
			}
		}
		return nil
	})
}

func TestConfigExampleParses(t *testing.T) {
	cfg, err := config.Load(filepath.Join(root, "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) < 3 || cfg.DefaultProvider != "fake" {
		t.Fatalf("config.example.toml 内容异常: %+v", cfg)
	}
	for _, p := range cfg.Providers {
		if p.APIKey != "" {
			t.Errorf("示例配置不应包含 api_key 明文: %s", p.Name)
		}
	}
}
