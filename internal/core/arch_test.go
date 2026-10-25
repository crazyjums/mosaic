package core

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestCoreImportBoundaries 是架构守卫：internal/core 的非测试代码只能依赖契约层。
//
// 对应验收标准 3、4：内核不导入 Wails、不导入任何具体厂商子包、不导入持久化实现。
// 这条规则写成测试而不是只写在文档里，是因为"不小心 import 了一下"是最常见的架构腐化方式。
func TestCoreImportBoundaries(t *testing.T) {
	const module = "github.com/crazyjums/myllmapp/"
	allowedInternal := map[string]bool{
		module + "internal/event":      true,
		module + "internal/provider":   true, // 只有接口与中立消息模型，不含具体厂商
		module + "internal/tool":       true,
		module + "internal/permission": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			switch {
			case strings.Contains(path, "wailsapp"):
				t.Errorf("%s 导入了 Wails: %s", name, path)
			case strings.HasPrefix(path, module) && !allowedInternal[path]:
				t.Errorf("%s 导入了不允许的内部包 %s（内核只能依赖契约层）", name, path)
			case !strings.HasPrefix(path, module) && strings.Contains(path, "."):
				// 内核只用标准库，第三方依赖应该放在适配层。
				t.Errorf("%s 导入了第三方包 %s", name, path)
			}
		}
	}
}
