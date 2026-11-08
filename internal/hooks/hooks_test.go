package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/mcp"
)

// 测试用“辅助进程”跑钩子：钩子命令就是这个测试程序自己（exec 形式），靠 HOOK_HELPER 和参数决定行为。
// 这样 Windows 上没有 sh 也能测。
func TestMain(m *testing.M) {
	if os.Getenv("HOOK_HELPER") == "1" {
		helper(os.Args[len(os.Args)-1])
		return
	}
	os.Exit(m.Run())
}

func helper(mode string) {
	in, _ := io.ReadAll(os.Stdin)
	kind, arg, _ := strings.Cut(mode, ":")
	switch kind {
	case "dump": // 把标准输入和环境写进文件
		_ = os.WriteFile(arg, in, 0o644)
		_ = os.WriteFile(arg+".env", []byte(strings.Join(os.Environ(), "\n")), 0o644)
		_ = os.WriteFile(arg+".cwd", []byte(mustWd()), 0o644)
	case "out":
		fmt.Print(arg)
	case "block":
		fmt.Fprint(os.Stderr, arg)
		os.Exit(2)
	case "exit":
		fmt.Fprint(os.Stderr, "boom")
		os.Exit(3)
	case "sleep":
		time.Sleep(5 * time.Second)
	}
}

func mustWd() string { d, _ := os.Getwd(); return d }

func newRunner() *Runner {
	r := New()
	// 真实的白名单（Windows 上要有 SystemRoot 等才能起进程）再加上辅助进程的开关。
	r.Env = func() []string { return append(mcp.BaseEnv(), "HOOK_HELPER=1") }
	r.Mode = func() string { return "accept_edits" }
	return r
}

func helperHook(ev core.HookEvent, matcher, mode string) Hook {
	return Hook{ID: mode, SourceName: "测试插件", Event: ev, Matcher: matcher, Command: os.Args[0], Args: []string{"-test.run=^$", mode}}
}

func preInput(tool, input string) core.HookInput {
	return core.HookInput{Event: core.HookPreToolUse, SessionID: "s1", ToolName: tool, ToolInput: json.RawMessage(input), ToolUseID: "c1"}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		m, tool string
		want    bool
	}{
		{"", "shell", true},
		{"*", "anything", true},
		{"Bash", "shell", true},
		{"shell", "shell", true},
		{"Write|Edit", "edit_file", true},
		{"Write|Edit", "read_file", false},
		{"Write, Edit", "write_file", true},
		{"mcp__fs__.*", "mcp__fs__read", true},
		{"mcp__fs__.*", "mcp__git__log", false},
		{"Bas", "shell", false},          // 精确列表不做前缀匹配
		{"Notebook.*", "Notebook", true}, // 正则整体匹配
		{"(", "shell", false},            // 写错的正则什么都不匹配
	}
	for _, c := range cases {
		if got := Matches(c.m, c.tool); got != c.want {
			t.Errorf("Matches(%q, %q) = %v", c.m, c.tool, got)
		}
	}
}

func TestStdinFieldsAndEnv(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "in.json")
	r := newRunner()
	t.Setenv("ANTHROPIC_API_KEY", "should-not-leak")
	h := helperHook(core.HookPreToolUse, "Bash", "dump:"+dump)
	h.PluginRoot, h.PluginData = "/p/root", "/p/data"
	r.ReplaceSource("plugin:t", []Hook{h})
	in := preInput("shell", `{"command":"ls"}`)
	in.Cwd = dir
	out := r.Run(context.Background(), in)
	if len(out.Runs) != 1 || out.Runs[0].Outcome != "ok" || out.Runs[0].Source != "plugin:t" {
		t.Fatalf("%+v", out.Runs)
	}
	var got map[string]any
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &got)
	if got["hook_event_name"] != "PreToolUse" || got["tool_name"] != "Bash" || got["mosaic_tool_name"] != "shell" || got["session_id"] != "s1" ||
		got["permission_mode"] != "acceptEdits" || got["tool_use_id"] != "c1" || got["cwd"] != dir {
		t.Fatalf("%v", got)
	}
	if ti, _ := got["tool_input"].(map[string]any); ti["command"] != "ls" {
		t.Fatalf("tool_input: %v", got["tool_input"])
	}
	env, _ := os.ReadFile(dump + ".env")
	if strings.Contains(string(env), "should-not-leak") {
		t.Fatal("API Key 不能传给钩子")
	}
	if !strings.Contains(string(env), "CLAUDE_PLUGIN_ROOT=/p/root") || !strings.Contains(string(env), "CLAUDE_PROJECT_DIR="+dir) {
		t.Fatalf("插件变量要作为环境变量传入: %s", env)
	}
	if cwd, _ := os.ReadFile(dump + ".cwd"); !sameDir(string(cwd), dir) {
		t.Fatalf("工作目录应是工作区: %s", cwd)
	}
}

func sameDir(a, b string) bool {
	ea, _ := filepath.EvalSymlinks(a)
	eb, _ := filepath.EvalSymlinks(b)
	return ea == eb
}

func TestPostToolUseAndOtherEventsInput(t *testing.T) {
	dir := t.TempDir()
	r := newRunner()
	for _, ev := range []core.HookEvent{core.HookPostToolUse, core.HookUserPromptSubmit, core.HookStop} {
		r.ReplaceSource("plugin:"+string(ev), []Hook{helperHook(ev, "", "dump:"+filepath.Join(dir, string(ev)))})
	}
	r.Run(context.Background(), core.HookInput{Event: core.HookPostToolUse, ToolName: "read_file", ToolInput: json.RawMessage(`{}`), HasToolResponse: true, ToolResponse: "内容"})
	r.Run(context.Background(), core.HookInput{Event: core.HookUserPromptSubmit, Prompt: "你好"})
	r.Run(context.Background(), core.HookInput{Event: core.HookStop, LastAssistantMessage: "做完了", StopHookActive: true})
	read := func(name string) map[string]any {
		var m map[string]any
		b, _ := os.ReadFile(filepath.Join(dir, name))
		_ = json.Unmarshal(b, &m)
		return m
	}
	if resp, _ := read("PostToolUse")["tool_response"].(map[string]any); resp["output"] != "内容" {
		t.Fatalf("%v", read("PostToolUse"))
	}
	if read("UserPromptSubmit")["prompt"] != "你好" {
		t.Fatal("prompt")
	}
	if s := read("Stop"); s["last_assistant_message"] != "做完了" || s["stop_hook_active"] != true {
		t.Fatalf("%v", s)
	}

	// 私密工具：不给内容，标明 redacted
	r.Run(context.Background(), core.HookInput{Event: core.HookPostToolUse, ToolName: "read_file", ToolInput: json.RawMessage(`{}`)})
	if resp, _ := read("PostToolUse")["tool_response"].(map[string]any); resp["output"] != "" || resp["redacted"] != true {
		t.Fatalf("%v", resp)
	}
}

func TestExitCodesAndJSON(t *testing.T) {
	run := func(ev core.HookEvent, modes ...string) core.HookOutcome {
		r := newRunner()
		var hs []Hook
		for _, m := range modes {
			hs = append(hs, helperHook(ev, "", m))
		}
		r.ReplaceSource("plugin:t", hs)
		in := preInput("shell", `{"command":"rm -rf /"}`)
		in.Event = ev
		return r.Run(context.Background(), in)
	}
	t.Run("退出码 2 = 阻止，stderr 是原因", func(t *testing.T) {
		o := run(core.HookPreToolUse, "block:不许删根目录")
		if !o.Block || o.Reason != "不许删根目录" || o.Runs[0].Outcome != "blocked" {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("其他退出码 = 出错但不阻止", func(t *testing.T) {
		o := run(core.HookPreToolUse, "exit:")
		if o.Block || o.Runs[0].Outcome != "error" || o.Runs[0].Message != "boom" {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("blockOnFailure：出错就阻止", func(t *testing.T) {
		r := newRunner()
		h := helperHook(core.HookPreToolUse, "", "exit:")
		h.BlockOnFailure = true
		r.ReplaceSource("plugin:t", []Hook{h})
		if o := r.Run(context.Background(), preInput("shell", `{}`)); !o.Block {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("allow 被忽略，并在事件里说明", func(t *testing.T) {
		o := run(core.HookPreToolUse, `out:{"hookSpecificOutput":{"permissionDecision":"allow"}}`)
		if o.Block || o.Ask || !strings.Contains(o.Runs[0].Message, "不会因此跳过确认") {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("deny / ask", func(t *testing.T) {
		if o := run(core.HookPreToolUse, `out:{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"危险"}}`); !o.Block || o.Reason != "危险" {
			t.Fatalf("%+v", o)
		}
		if o := run(core.HookPreToolUse, `out:{"hookSpecificOutput":{"permissionDecision":"ask"}}`); o.Block || !o.Ask || o.Runs[0].Outcome != "ask" {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("updatedInput 依次传给后面的钩子；不是对象就忽略", func(t *testing.T) {
		dir := t.TempDir()
		dump := filepath.Join(dir, "second")
		o := run(core.HookPreToolUse, `out:{"hookSpecificOutput":{"updatedInput":{"command":"ls"}}}`, "dump:"+dump)
		if string(o.UpdatedInput) != `{"command":"ls"}` || !o.Runs[0].UpdatedInput {
			t.Fatalf("%+v", o)
		}
		b, _ := os.ReadFile(dump)
		if !strings.Contains(string(b), `"command":"ls"`) {
			t.Fatalf("第二条钩子应看到改过的参数: %s", b)
		}
		if o := run(core.HookPreToolUse, `out:{"hookSpecificOutput":{"updatedInput":"rm"}}`); o.UpdatedInput != nil {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("PreToolUse 被阻止后不再跑后面的钩子", func(t *testing.T) {
		dir := t.TempDir()
		dump := filepath.Join(dir, "second")
		o := run(core.HookPreToolUse, "block:no", "dump:"+dump)
		if len(o.Runs) != 1 {
			t.Fatalf("%+v", o.Runs)
		}
		if _, err := os.Stat(dump); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("被阻止后的钩子不应运行")
		}
	})
	t.Run("continue:false、decision:block、additionalContext 带标注", func(t *testing.T) {
		o := run(core.HookPostToolUse, `out:{"continue":false,"stopReason":"到此为止","decision":"block","reason":"格式错","hookSpecificOutput":{"additionalContext":"记得跑测试"}}`)
		if !o.StopTurn || o.StopReason != "到此为止" || !o.Block || o.Reason != "格式错" || len(o.Context) != 1 ||
			!strings.Contains(o.Context[0], "测试插件") || !strings.Contains(o.Context[0], "记得跑测试") {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("UserPromptSubmit 的纯文本输出是上下文，其余事件忽略", func(t *testing.T) {
		if o := run(core.HookUserPromptSubmit, "out:今天周日"); len(o.Context) != 1 || !strings.Contains(o.Context[0], "今天周日") {
			t.Fatalf("%+v", o)
		}
		if o := run(core.HookPreToolUse, "out:随便说点"); len(o.Context) != 0 {
			t.Fatalf("%+v", o)
		}
	})
	t.Run("坏 JSON 是出错，不阻止", func(t *testing.T) {
		if o := run(core.HookPreToolUse, "out:{not json"); o.Block || o.Runs[0].Outcome != "error" {
			t.Fatalf("%+v", o)
		}
	})
}

func TestTimeoutAndCancel(t *testing.T) {
	r := newRunner()
	h := helperHook(core.HookPreToolUse, "", "sleep:")
	h.Timeout = 300 * time.Millisecond
	r.ReplaceSource("plugin:t", []Hook{h})
	start := time.Now()
	o := r.Run(context.Background(), preInput("shell", `{}`))
	if o.Block || o.Runs[0].Outcome != "timeout" || time.Since(start) > 4*time.Second {
		t.Fatalf("%+v %v", o, time.Since(start))
	}

	h.BlockOnFailure = true
	r.ReplaceSource("plugin:t", []Hook{h})
	if o := r.Run(context.Background(), preInput("shell", `{}`)); !o.Block {
		t.Fatalf("超时 + blockOnFailure 应阻止: %+v", o)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.Timeout = 0
	r.ReplaceSource("plugin:t", []Hook{h})
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	if o := r.Run(ctx, preInput("shell", `{}`)); o.Block || o.Runs[0].Message != "已取消" {
		t.Fatalf("取消不算失败: %+v", o)
	}
}

func TestSwitchesAndSources(t *testing.T) {
	r := newRunner()
	r.ReplaceSource("plugin:a", []Hook{helperHook(core.HookPreToolUse, "Bash", "block:a")})
	r.ReplaceSource("plugin:b", []Hook{helperHook(core.HookPreToolUse, "Write", "block:b")})
	if r.Count() != 2 {
		t.Fatal(r.Count())
	}
	if o := r.Run(context.Background(), preInput("read_file", `{}`)); len(o.Runs) != 0 {
		t.Fatal("不匹配的工具不跑钩子")
	}
	if o := r.Run(context.Background(), preInput("shell", `{}`)); o.Reason != "a" {
		t.Fatalf("%+v", o)
	}
	r.SetDisabled(true)
	if o := r.Run(context.Background(), preInput("shell", `{}`)); len(o.Runs) != 0 {
		t.Fatal("总开关关掉后不运行")
	}
	r.SetDisabled(false)
	r.ReplaceSource("plugin:a", nil)
	if o := r.Run(context.Background(), preInput("shell", `{}`)); len(o.Runs) != 0 || r.Count() != 1 {
		t.Fatal("清空来源后不运行")
	}
}

func TestShellFormNeedsSh(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	defer func() { lookPath = old }()
	r := newRunner()
	r.ReplaceSource("plugin:t", []Hook{{Event: core.HookPreToolUse, Command: "echo hi"}})
	o := r.Run(context.Background(), preInput("shell", `{}`))
	if o.Block || o.Runs[0].Outcome != "error" || !strings.Contains(o.Runs[0].Message, "sh") {
		t.Fatalf("%+v", o)
	}
}

func TestShellFormExpandsVars(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("没有 sh")
	}
	dir := t.TempDir()
	r := newRunner()
	r.ReplaceSource("plugin:t", []Hook{{Event: core.HookUserPromptSubmit, Command: `cat >/dev/null; printf '%s' "${CLAUDE_PLUGIN_ROOT}"`, PluginRoot: dir}})
	o := r.Run(context.Background(), core.HookInput{Event: core.HookUserPromptSubmit, Prompt: "x"})
	if len(o.Context) != 1 || !strings.Contains(o.Context[0], dir) {
		t.Fatalf("%+v", o)
	}
}

func TestSubagentEvents(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "in.json")
	r := newRunner()
	r.ReplaceSource("plugin:t", []Hook{helperHook(core.HookSubagentStop, "reviewer", "dump:"+dump), helperHook(core.HookSubagentStart, "explorer", "block:x")})
	// 插件子 agent 的名字带前缀，精确匹配也认去掉前缀后的名字
	o := r.Run(context.Background(), core.HookInput{Event: core.HookSubagentStop, AgentID: "agent_1", AgentType: "deploy:reviewer", LastAssistantMessage: "好了", StopHookActive: true})
	if len(o.Runs) != 1 {
		t.Fatalf("%+v", o)
	}
	var m map[string]any
	b, _ := os.ReadFile(dump)
	_ = json.Unmarshal(b, &m)
	if m["agent_type"] != "deploy:reviewer" || m["agent_id"] != "agent_1" || m["last_assistant_message"] != "好了" || m["stop_hook_active"] != true {
		t.Fatalf("%v", m)
	}
	if o := r.Run(context.Background(), core.HookInput{Event: core.HookSubagentStart, AgentType: "reviewer"}); len(o.Runs) != 0 {
		t.Fatal("matcher 不匹配的子 agent 不跑")
func TestShellFormDoesNotInjectPaths(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Skip("没有 sh")
	}
	// 目录名里带 $(…)：先替换进命令文本的话会被 sh 当成命令执行
	root := filepath.Join(t.TempDir(), "plugin-$(echo INJECTED)")
	r := newRunner()
	r.ReplaceSource("plugin:t", []Hook{{Event: core.HookUserPromptSubmit, Command: `cat >/dev/null; printf '%s' "${CLAUDE_PLUGIN_ROOT}"`, PluginRoot: root}})
	o := r.Run(context.Background(), core.HookInput{Event: core.HookUserPromptSubmit, Prompt: "x"})
	if len(o.Context) != 1 || !strings.Contains(o.Context[0], "$(echo INJECTED)") {
		t.Fatalf("路径应原样交给命令，不被执行: %+v", o)
	}
}

func TestBadJSONWithBlockOnFailure(t *testing.T) {
	r := newRunner()
	h := helperHook(core.HookPreToolUse, "", "out:{not json")
	h.BlockOnFailure = true
	r.ReplaceSource("plugin:t", []Hook{h})
	if o := r.Run(context.Background(), preInput("shell", `{}`)); !o.Block || o.Runs[0].Outcome != "blocked" {
		t.Fatalf("坏 JSON + BlockOnFailure 应阻止: %+v", o)
	}
}
