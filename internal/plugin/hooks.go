package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 插件钩子（Claude Code 格式的 hooks/hooks.json 和清单里的 hooks）。
//
// 钩子是会在用户电脑上执行任意命令的东西，而且站在权限系统旁边，所以：
//   - 导入时只记下来，不会自动运行：每一条都要用户在安装确认里单独勾选（默认不勾），之后在详情里也能单独关掉；
//   - 批准认的是钩子的“指纹”（事件 + 匹配条件 + 命令 + 时限）：插件更新后钩子变了，指纹就变了，需要重新批准；
//   - 设置里有一个“停用所有钩子”的总开关。
// 执行和“只能更严”的规则在 internal/hooks 和 internal/core/hooks.go。

// HookDecl 是插件声明的一条钩子。
type HookDecl struct {
	// ID 是指纹（见上），批准和单独关闭都用它。
	ID      string `json:"id"`
	Event   string `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Command string `json:"command"`
	// Timeout 是秒数，0 表示默认。
	Timeout int `json:"timeout,omitempty"`
	// File 是声明它的文件（相对插件目录），显示用。
	File string `json:"file,omitempty"`
}

// supportedHookEvents 是 Mosaic 支持的钩子事件，其余的列为“不会生效”。
var supportedHookEvents = map[string]bool{
	"PreToolUse": true, "PostToolUse": true, "UserPromptSubmit": true, "Stop": true,
	"SubagentStart": true, "SubagentStop": true,
}

const maxPluginHooks = 50

// hookFingerprint 是钩子的指纹。命令、匹配条件、事件、时限任何一项变了都算一条新钩子。
func hookFingerprint(event, matcher, command string, timeout int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", event, matcher, command, timeout)))
	return hex.EncodeToString(sum[:])[:16]
}

// hooks 读取插件的钩子：默认的 hooks/hooks.json，加上清单里 hooks 写的文件或内联配置。
func (c *claudeImport) hooks(cm claudeManifest) {
	type source struct {
		file string
		raw  []byte
	}
	var sources []source
	seen := map[string]bool{}
	addFile := func(rel string) {
		rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
		if seen[rel] {
			return
		}
		seen[rel] = true
		p, err := insideDir(c.dir, rel)
		if err != nil {
			c.ignore("钩子文件 %s：%v", rel, err)
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			c.ignore("钩子文件 %s 读不了：%v", rel, err)
			return
		}
		sources = append(sources, source{rel, data})
	}
	if fileExists(filepath.Join(c.dir, "hooks", "hooks.json")) {
		addFile("hooks/hooks.json")
	}
	if raw := []byte(strings.TrimSpace(string(cm.Hooks))); len(raw) > 0 && string(raw) != "null" {
		if paths, ok := rawStrings(cm.Hooks); ok {
			for _, p := range paths {
				addFile(p)
			}
		} else if raw[0] == '{' {
			sources = append(sources, source{"plugin.json", raw})
		} else {
			c.ignore("清单里的 hooks 写法不被支持（只能是文件路径、路径列表或内联配置）")
		}
	}
	for _, s := range sources {
		c.parseHooks(s.file, s.raw)
	}
}

// parseHooks 解析一份钩子配置：{"hooks": {事件: [{matcher, hooks: [{type, command, timeout}]}]}}，
// 也接受省掉外层 "hooks" 的写法。
func (c *claudeImport) parseHooks(file string, raw []byte) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		c.ignore("钩子文件 %s 不是合法的 JSON：%v", file, err)
		return
	}
	events := outer
	if inner, ok := outer["hooks"]; ok {
		events = nil
		if err := json.Unmarshal(inner, &events); err != nil {
			c.ignore("钩子文件 %s 的 hooks 写法不对：%v", file, err)
			return
		}
	}
	names := make([]string, 0, len(events))
	for name := range events {
		if name != "description" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, ev := range names {
		if !supportedHookEvents[ev] {
			c.ignore("钩子事件 %s（%s）：Mosaic 不支持这个事件", ev, file)
			continue
		}
		var groups []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string  `json:"type"`
				Command string  `json:"command"`
				Timeout float64 `json:"timeout"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(events[ev], &groups); err != nil {
			c.ignore("钩子事件 %s（%s）的写法不对：%v", ev, file, err)
			continue
		}
		for _, g := range groups {
			for _, h := range g.Hooks {
				typ := h.Type
				if typ == "" {
					typ = "command"
				}
				if typ != "command" {
					c.ignore("%s 钩子的 %s 类型（%s）：Mosaic 只支持 command", ev, typ, file)
					continue
				}
				cmd := strings.TrimSpace(h.Command)
				if cmd == "" {
					continue
				}
				if len(c.m.HookDecls) >= maxPluginHooks {
					c.ignore("钩子超过 %d 条，其余的没有导入", maxPluginHooks)
					return
				}
				timeout := int(h.Timeout)
				id := hookFingerprint(ev, g.Matcher, cmd, timeout)
				dup := false
				for _, x := range c.m.HookDecls {
					dup = dup || x.ID == id
				}
				if !dup {
					c.m.HookDecls = append(c.m.HookDecls, HookDecl{ID: id, Event: ev, Matcher: g.Matcher, Command: cmd, Timeout: timeout, File: file})
				}
			}
		}
	}
}
