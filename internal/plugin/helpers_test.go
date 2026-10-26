package plugin

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/crazyjums/myllmapp/internal/mcp"
	"github.com/crazyjums/myllmapp/internal/secret"
)

// fakeMCP 记录 Host 推送给 MCP 管理器的内容。
type fakeMCP struct {
	mu   sync.Mutex
	by   map[string][]mcp.ServerConfig
	fail map[string]error
}

func newFakeMCP() *fakeMCP {
	return &fakeMCP{by: map[string][]mcp.ServerConfig{}, fail: map[string]error{}}
}

func (f *fakeMCP) ApplySource(source string, cfgs []mcp.ServerConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.by[source] = cfgs
	return f.fail[source]
}

func (f *fakeMCP) get(source string) []mcp.ServerConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.by[source]
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writePlugin 在 dir 下写一个插件包：清单 + 一个技能 + 一个命令；manifest 由调用方给。
func writePlugin(t *testing.T, dir string, manifest map[string]any) {
	t.Helper()
	b, _ := json.MarshalIndent(manifest, "", "  ")
	writeFile(t, filepath.Join(dir, FileName), string(b))
	writeFile(t, filepath.Join(dir, "skills", "docs", "SKILL.md"), "---\nname: demo-docs\ndescription: 读文档\n---\n正文")
	writeFile(t, filepath.Join(dir, "commands", "sum.md"), "---\ndescription: 总结\n---\n请总结：{{args}}")
}

// baseManifest 返回一个合法的、带技能 / 命令 / MCP / 配置的清单。
func baseManifest(id string) map[string]any {
	return map[string]any{
		"id": id, "name": "演示 " + id, "version": "0.1.0", "description": "测试用",
		"skills":   []any{map[string]any{"path": "skills/docs"}},
		"commands": []any{map[string]any{"name": "sum", "path": "commands/sum.md"}},
		"mcp": []any{map[string]any{
			"command": "node", "args": []any{"${PLUGIN_DIR}/server.js", "--profile", "${config.profile}"},
			"env": map[string]any{"TOKEN": "${config.token}", "DATA": "${DATA_DIR}"}, "risk": "read",
		}},
		"config": []any{
			map[string]any{"key": "profile", "label": "配置名", "type": "string", "default": "default"},
			map[string]any{"key": "token", "label": "令牌", "type": "secret", "required": true},
			map[string]any{"key": "region", "label": "区域", "type": "select", "options": []any{"cn", "us"}, "default": "cn"},
		},
	}
}

func newTestHost(t *testing.T) (*Host, *fakeMCP, *secret.Memory, string) {
	t.Helper()
	root := t.TempDir()
	mem := secret.NewMemory()
	f := newFakeMCP()
	h, err := NewHost(HostOptions{
		Dir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "plugin-data"), StateFile: filepath.Join(root, "plugins.json"),
		Secrets: mem, MCP: f, AppVersion: "1.3.2",
		LookPath: func(name string) (string, error) {
			if name == "present-cmd" {
				return "/bin/present-cmd", nil
			}
			return "", os.ErrNotExist
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, f, mem, root
}

// makeZip 把 dir 打成 zip；extra 里是额外的条目（用来构造恶意包）。
func makeZip(t *testing.T, dir, out string, prefix string, extra map[string]string) {
	t.Helper()
	zf, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		w, _ := zw.Create(prefix + filepath.ToSlash(rel))
		b, _ := os.ReadFile(p)
		w.Write(b)
		return nil
	})
	for name, content := range extra {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	zw.Close()
	zf.Close()
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("应包含 %q: %q", sub, s)
	}
}
