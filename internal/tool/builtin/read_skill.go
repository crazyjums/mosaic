package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/skill"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// ReadSkill 读取一个技能的正文，或技能文件夹里的一个资源文件。
//
// 技能可能放在工作区外面（~/.claude/skills），read_file 读不到，所以单独做一个工具；
// 它只能读技能文件夹内的文件（见 skill.ReadResource），所以是只读、免确认的。
// 技能里让模型执行脚本时，仍然要经过 shell 工具的确认。
type ReadSkill struct {
	// home 是用户目录；为空表示不搜索个人技能（测试里用）。
	home string
}

// NewReadSkill 创建工具。home 通常是 os.UserHomeDir() 的结果。
func NewReadSkill(home string) *ReadSkill { return &ReadSkill{home: home} }

type readSkillInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (t *ReadSkill) Spec() tool.Spec {
	return tool.Spec{
		Name:        "read_skill",
		Description: "读取一个技能（用户写好的可复用说明）。只传 name：返回技能正文，并列出文件夹里的资源文件；再传 path：读取其中一个资源文件。技能清单见系统提示词里的 Skills；用户以 /技能名 开头的消息就是要求使用该技能。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "name": {"type": "string", "description": "技能名，来自系统提示词里的技能清单"},
    "path": {"type": "string", "description": "可选。技能文件夹内的资源文件，相对路径，例如 reference.md 或 scripts/run.py"}
  },
  "required": ["name"]
}`),
		Risk: permission.RiskRead,
	}
}

func (t *ReadSkill) Summarize(input json.RawMessage) string {
	var in readSkillInput
	_ = json.Unmarshal(input, &in)
	if in.Path != "" {
		return fmt.Sprintf("读取技能 %s 的 %s", in.Name, in.Path)
	}
	return fmt.Sprintf("读取技能 %s", in.Name)
}

func (t *ReadSkill) Execute(_ context.Context, call tool.Call) tool.Result {
	var in readSkillInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("参数无效：%v", err)
	}
	s, ok := skill.Find(call.Workspace, t.home, in.Name)
	if !ok {
		var names []string
		for _, x := range skill.Discover(call.Workspace, t.home) {
			names = append(names, x.Name)
		}
		if len(names) == 0 {
			return tool.Errorf("没有找到技能 %q：当前没有任何可用的技能", in.Name)
		}
		return tool.Errorf("没有找到技能 %q。可用的技能：%s", in.Name, strings.Join(names, "、"))
	}
	if in.Path != "" {
		text, err := skill.ReadResource(s, in.Path)
		if err != nil {
			return tool.Errorf("读取技能资源 %s 失败：%v。可以先不带 path 调用，看看这个技能有哪些文件", in.Path, err)
		}
		return tool.Result{Output: text}
	}
	body, err := skill.Body(s)
	if err != nil {
		return tool.Errorf("读取技能 %s 失败：%v", s.Name, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skill: %s\nDirectory: %s\n\n%s\n", s.Name, s.Dir, body)
	if files := skill.Files(s); len(files) > 0 {
		b.WriteString("\nFiles in this skill (read one with read_skill {name, path}; run scripts with shell using the absolute path under Directory):\n")
		for _, f := range files {
			b.WriteString("- " + f + "\n")
		}
	}
	return tool.Result{Output: strings.TrimRight(b.String(), "\n")}
}
