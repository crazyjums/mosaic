package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Scaffold 在 parent 目录下创建一个可以直接安装的最小插件（一个技能加一个斜杠命令），返回新目录的路径。
// 给想写插件但不知道从哪开始的人：改里面的文字，再用“安装文件夹…”装上，就能看到效果。
// 只在 parent 下新建目录，已存在同名目录时拒绝，不会覆盖用户的文件。
func Scaffold(parent, id string) (string, error) {
	id = strings.TrimSpace(id)
	if !idRe.MatchString(id) {
		return "", errors.New("插件 ID 只能用小写字母、数字和 -，以字母或数字开头，最长 24 个字符")
	}
	if fi, err := os.Stat(parent); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("找不到目录 %s", parent)
	}
	dir := filepath.Join(parent, id)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("%s 已经存在，换一个 ID 或先删掉它", dir)
	}
	files := map[string]string{
		FileName: fmt.Sprintf(`{
  "id": %q,
  "name": %q,
  "version": "0.1.0",
  "description": "一句话说明这个插件做什么。",
  "protocol": 1,
  "skills": [{ "path": "skills/%s" }],
  "commands": [{ "name": %q, "path": "commands/%s.md", "description": "一句话说明这个命令做什么" }]
}
`, id, id, id, id, id),
		filepath.Join("skills", id, "SKILL.md"): fmt.Sprintf(`---
name: %s
description: 写清楚“用户在什么情况下该用这个技能”，模型靠这句话决定要不要读下面的正文。
---

在这里写具体的做法：一步一步，越具体越好。例如：

1. 先确认……
2. 然后……
3. 输出格式是……
`, id),
		filepath.Join("commands", id+".md"): `---
description: 一句话说明这个命令做什么
---

在这里写发给模型的提示词。用户在命令后面输入的内容会替换下面的参数：

{{args}}
`,
		"README.md": fmt.Sprintf(`# %s

由“新建插件”生成的最小插件：一个技能加一个斜杠命令，没有可执行代码。

1. 改 `+"`plugin.json`"+` 里的 name 和 description；
2. 改 `+"`skills/%s/SKILL.md`"+`（技能）和 `+"`commands/%s.md`"+`（命令 /%s）；
3. 设置 → 插件 → “安装文件夹…”，选这个目录，确认后就能用；改完文件再装一次即可更新。

想加外部工具（MCP）、配置表单、状态检查，见仓库的 docs/plugin-development.md。
`, id, id, id, id),
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}
