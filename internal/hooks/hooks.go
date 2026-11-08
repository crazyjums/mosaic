package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
)

// Hook 是一条钩子。
type Hook struct {
	// ID 在来源内唯一且稳定（批准、单独关闭时用它认人，见 H2）。
	ID string
	// Source 是来源（"plugin:<id>"），SourceName 是给人看的名字。
	Source     string
	SourceName string

	Event   core.HookEvent
	Matcher string
	// Command 是命令。Args 为 nil 时 Command 交给 sh -c 执行（和 Claude Code 一样）；
	// Args 不为 nil 时直接执行 Command 这个程序、参数是 Args，不经过 shell（Windows 上没有 sh 也能跑）。
	Command string
	Args    []string
	// Timeout 为 0 时用默认值（30 秒；UserPromptSubmit 10 秒）。
	Timeout time.Duration
	// BlockOnFailure：PreToolUse 上这条钩子出错或超时就阻止（更严）。默认出错不阻止。
	BlockOnFailure bool

	// PluginRoot / PluginData 用来替换 ${CLAUDE_PLUGIN_ROOT} / ${CLAUDE_PLUGIN_DATA}，也作为同名环境变量传给命令。
	PluginRoot string
	PluginData string
}

// Display 是给人看的命令行（确认页、事件里显示原样命令）。
func (h Hook) Display() string {
	if h.Args == nil {
		return h.Command
	}
	return strings.TrimSpace(h.Command + " " + strings.Join(h.Args, " "))
}

const (
	defaultTimeout      = 30 * time.Second
	defaultPromptTimout = 10 * time.Second
	maxTimeout          = 120 * time.Second
	maxOutputRunes      = 10000
)

// Runner 保存所有钩子并实现 core.Hooks。
type Runner struct {
	mu       sync.RWMutex
	bySource map[string][]Hook
	disabled bool

	// Mode 返回当前权限档位（Mosaic 的取值），转成 Claude Code 的 permission_mode 交给钩子。可为 nil。
	Mode func() string
	// Env 返回基础环境变量；为 nil 时用 mcp.BaseEnv（见 exec.go）。
	Env func() []string
}

// New 创建一个没有钩子的 Runner。
func New() *Runner { return &Runner{bySource: map[string][]Hook{}} }

// ReplaceSource 用 hs 替换来源 source 的全部钩子（插件启停、批准变化时调用）。hs 为空即清空。
func (r *Runner) ReplaceSource(source string, hs []Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(hs) == 0 {
		delete(r.bySource, source)
		return
	}
	cp := make([]Hook, len(hs))
	for i, h := range hs {
		h.Source = source
		cp[i] = h
	}
	r.bySource[source] = cp
}

// SetDisabled 是总开关：关掉之后所有钩子都不运行。
func (r *Runner) SetDisabled(off bool) {
	r.mu.Lock()
	r.disabled = off
	r.mu.Unlock()
}

// Disabled 报告总开关是否关着。
func (r *Runner) Disabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.disabled
}

// Count 返回当前登记的钩子数（设置页显示用）。
func (r *Runner) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, hs := range r.bySource {
		n += len(hs)
	}
	return n
}

// matching 返回这个事件上匹配的钩子，按来源名、再按登记顺序排列（结果稳定，便于理解“谁先跑”）。
func (r *Runner) matching(ev core.HookEvent, toolName string) []Hook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.disabled {
		return nil
	}
	sources := make([]string, 0, len(r.bySource))
	for s := range r.bySource {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	var out []Hook
	for _, s := range sources {
		for _, h := range r.bySource[s] {
			if h.Event != ev {
				continue
			}
			if (ev == core.HookPreToolUse || ev == core.HookPostToolUse) && !Matches(h.Matcher, toolName) {
				continue
			}
			out = append(out, h)
		}
	}
	return out
}

// Run 实现 core.Hooks。
func (r *Runner) Run(ctx context.Context, in core.HookInput) core.HookOutcome {
	hs := r.matching(in.Event, in.ToolName)
	var out core.HookOutcome
	if len(hs) == 0 {
		return out
	}
	mode := ""
	if r.Mode != nil {
		mode = claudeMode(r.Mode())
	}
	for _, h := range hs {
		if ctx.Err() != nil {
			break
		}
		start := time.Now()
		res := r.exec(ctx, h, stdinJSON(in, mode), in.Cwd)
		run := event.HookRun{Source: h.Source, SourceName: h.SourceName, Command: h.Display(), Outcome: "ok", DurationMS: time.Since(start).Milliseconds()}
		apply(&out, &in, h, res, &run)
		out.Runs = append(out.Runs, run)
		// 已经被阻止或要求停下的，后面的钩子不用再跑（PreToolUse / UserPromptSubmit：结论不会再变；
		// 跑下去只会让更多命令看到这次被拦下的参数）。
		if (out.Block || out.StopTurn) && (in.Event == core.HookPreToolUse || in.Event == core.HookUserPromptSubmit) {
			break
		}
	}
	return out
}

// claudeMode 把 Mosaic 的权限档位换成 Claude Code 的 permission_mode 取值。
func claudeMode(m string) string {
	switch m {
	case "readonly":
		return "plan"
	case "accept_edits":
		return "acceptEdits"
	case "auto":
		return "bypassPermissions"
	default:
		return "default"
	}
}

// claudeToolNames 是 Mosaic 内置工具在 Claude Code 里的名字。钩子是照 Claude Code 写的（matcher 写 "Bash"、
// 脚本里判断 tool_name == "Write"），所以匹配时两种名字都认，交给钩子的 tool_name 用 Claude Code 的名字。
var claudeToolNames = map[string]string{
	"shell":          "Bash",
	"read_file":      "Read",
	"write_file":     "Write",
	"edit_file":      "Edit",
	"find_files":     "Glob",
	"search_content": "Grep",
	"list_dir":       "LS",
	"todo_write":     "TodoWrite",
	"task":           "Task",
}

// ClaudeToolName 返回工具在 Claude Code 里的名字；没有对应的就原样返回（MCP 工具两边都是 mcp__服务__工具）。
func ClaudeToolName(name string) string {
	if n, ok := claudeToolNames[name]; ok {
		return n
	}
	return name
}

var exactListRe = regexp.MustCompile(`^[A-Za-z0-9_|,\s]+$`)

// Matches 按 Claude Code 的规则判断 matcher 是否匹配这个工具：空或 "*" 匹配全部；只含字母、数字、下划线和
// | , 的是精确名字列表；其余当正则（整体匹配）。Mosaic 名字和 Claude Code 名字任一匹配即可。
// 写错的正则不匹配任何工具（宁可不跑，也不要误跑到所有工具上）。
func Matches(matcher, tool string) bool {
	matcher = strings.TrimSpace(matcher)
	if matcher == "" || matcher == "*" {
		return true
	}
	names := []string{tool, ClaudeToolName(tool)}
	if exactListRe.MatchString(matcher) {
		for _, m := range strings.FieldsFunc(matcher, func(r rune) bool { return r == '|' || r == ',' }) {
			for _, n := range names {
				if strings.TrimSpace(m) == n {
					return true
				}
			}
		}
		return false
	}
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	if err != nil {
		return false
	}
	for _, n := range names {
		if re.MatchString(n) {
			return true
		}
	}
	return false
}

// stdinJSON 生成交给钩子的标准输入（Claude Code 的字段名）。
func stdinJSON(in core.HookInput, mode string) map[string]any {
	m := map[string]any{
		"session_id":      in.SessionID,
		"transcript_path": "", // Mosaic 的会话不是 Claude Code 的 JSONL 文件，不提供
		"cwd":             in.Cwd,
		"hook_event_name": string(in.Event),
	}
	if mode != "" {
		m["permission_mode"] = mode
	}
	switch in.Event {
	case core.HookPreToolUse, core.HookPostToolUse:
		m["tool_name"] = ClaudeToolName(in.ToolName)
		m["mosaic_tool_name"] = in.ToolName
		m["tool_use_id"] = in.ToolUseID
		var input any = map[string]any{}
		if len(in.ToolInput) > 0 {
			_ = json.Unmarshal(in.ToolInput, &input)
		}
		m["tool_input"] = input
		if in.Event == core.HookPostToolUse {
			if in.HasToolResponse {
				m["tool_response"] = map[string]any{"output": in.ToolResponse, "is_error": in.ToolIsError}
			} else {
				// 私密工具：只告诉钩子“有结果、不给内容”，免得脚本把缺字段当成出错。
				m["tool_response"] = map[string]any{"output": "", "is_error": in.ToolIsError, "redacted": true}
			}
		}
	case core.HookUserPromptSubmit:
		m["prompt"] = in.Prompt
	case core.HookStop:
		m["stop_hook_active"] = in.StopHookActive
		m["last_assistant_message"] = in.LastAssistantMessage
	}
	return m
}

// hookOutput 是钩子标准输出里 Claude Code 格式的 JSON。
type hookOutput struct {
	Continue       *bool  `json:"continue"`
	StopReason     string `json:"stopReason"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason"`
	SystemMessage  string `json:"systemMessage"`
	SuppressOutput bool   `json:"suppressOutput"`
	Specific       struct {
		PermissionDecision       string          `json:"permissionDecision"`
		PermissionDecisionReason string          `json:"permissionDecisionReason"`
		UpdatedInput             json.RawMessage `json:"updatedInput"`
		AdditionalContext        string          `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// apply 把一条钩子的运行结果并入总结论，并填好这次运行的事件。
func apply(out *core.HookOutcome, in *core.HookInput, h Hook, res execResult, run *event.HookRun) {
	label := func(s string) string {
		name := h.SourceName
		if name == "" {
			name = h.Source
		}
		return fmt.Sprintf("【%s 的 %s 钩子】%s", name, in.Event, s)
	}
	fail := func(outcome, msg string) {
		run.Outcome, run.Message = outcome, msg
		if h.BlockOnFailure && in.Event == core.HookPreToolUse {
			run.Outcome = "blocked"
			out.Block, out.Reason = true, "钩子没有正常完成（"+msg+"），按它的设置阻止"
		}
	}
	switch {
	case res.cancelled:
		run.Outcome, run.Message = "error", "已取消"
		return
	case res.timedOut:
		fail("timeout", fmt.Sprintf("超过 %s 没有结束", res.timeout))
		return
	case res.startErr != nil:
		fail("error", res.startErr.Error())
		return
	case res.exitCode == 2:
		reason := strings.TrimSpace(res.stderr)
		if reason == "" {
			reason = "钩子以退出码 2 阻止，没有给出原因"
		}
		run.Outcome, run.Message = "blocked", reason
		out.Block, out.Reason = true, reason
		return
	case res.exitCode != 0:
		msg := strings.TrimSpace(res.stderr)
		if msg == "" {
			msg = fmt.Sprintf("退出码 %d", res.exitCode)
		}
		fail("error", msg)
		return
	}

	text := strings.TrimSpace(res.stdout)
	if !strings.HasPrefix(text, "{") {
		// 纯文本输出：只有 UserPromptSubmit 会把它当作上下文（和 Claude Code 一致），其余事件忽略。
		if text != "" && in.Event == core.HookUserPromptSubmit {
			out.Context = append(out.Context, label(text))
			run.AddedContext = true
		}
		return
	}
	var o hookOutput
	if err := json.Unmarshal([]byte(text), &o); err != nil {
		fail("error", "输出看起来是 JSON 但解析失败："+err.Error()) // 设了 BlockOnFailure 的 PreToolUse 照样阻止
		return
	}
	var notes []string
	if o.SystemMessage != "" {
		notes = append(notes, o.SystemMessage)
	}
	if o.Continue != nil && !*o.Continue {
		out.StopTurn, out.StopReason = true, orText(o.StopReason, "钩子要求停止")
		run.Outcome = "blocked"
		notes = append(notes, "要求停止这一轮："+out.StopReason)
	}
	if o.Decision == "block" {
		out.Block, out.Reason = true, orText(o.Reason, "钩子阻止了，没有给出原因")
		run.Outcome = "blocked"
		notes = append(notes, out.Reason)
	}
	switch o.Specific.PermissionDecision {
	case "deny":
		out.Block, out.Reason = true, orText(o.Specific.PermissionDecisionReason, "钩子拒绝了这次调用")
		run.Outcome = "blocked"
		notes = append(notes, out.Reason)
	case "ask":
		out.Ask = true
		if run.Outcome == "ok" {
			run.Outcome = "ask"
		}
		if o.Specific.PermissionDecisionReason != "" {
			notes = append(notes, o.Specific.PermissionDecisionReason)
		}
	case "allow":
		// 不跳过确认：钩子不能替用户放行。
		notes = append(notes, "钩子返回了 allow，Mosaic 不会因此跳过确认")
	}
	if len(o.Specific.UpdatedInput) > 0 && in.Event == core.HookPreToolUse && !out.Block {
		if obj := strings.TrimSpace(string(o.Specific.UpdatedInput)); strings.HasPrefix(obj, "{") && json.Valid([]byte(obj)) {
			out.UpdatedInput = json.RawMessage(obj)
			in.ToolInput = out.UpdatedInput // 后面的钩子看到改过之后的参数
			run.UpdatedInput = true
		} else {
			notes = append(notes, "updatedInput 不是 JSON 对象，已忽略")
		}
	}
	if c := strings.TrimSpace(o.Specific.AdditionalContext); c != "" && in.Event != core.HookStop {
		out.Context = append(out.Context, label(c))
		run.AddedContext = true
	}
	run.Message = strings.Join(notes, "；")
}

func orText(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
