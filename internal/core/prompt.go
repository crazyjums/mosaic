package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// DefaultSystemPrompt 生成默认系统提示词。
//
// 放在内核里而不是装配层：它描述的是 agent 的运行环境（工作区、操作系统），
// 工作区在运行时可以被修改（设置 → 常规），修改后下一轮的提示词要跟着变，由 Engine 在每轮开始时生成。
//
// 刻意保持简短：只交代环境事实和工具使用约定。
// 注意不放当前时间之类每次都变的内容，以免破坏厂商侧的提示缓存。
func DefaultSystemPrompt(workspace string) string {
	return fmt.Sprintf(`You are a helpful coding agent running inside a desktop client.

Environment:
- Workspace root: %s (all file paths are relative to it)
- OS: %s/%s
- %s

Tools:
- Explore: list_dir (recursive=true for a tree), find_files (by glob), search_content (regex, like grep), read_file (use offset/limit for large files).
- Change: edit_file for targeted edits (old_string must match exactly and be unique; read the file first), write_file for new files or full rewrites (mode=append to append), mkdir, move_file, delete_file (moves to %s, recoverable).
- Git (read-only): git_status, git_diff, git_log.
- shell for builds, tests and anything else (timeout_seconds, cwd). Prefer the dedicated tools over shell for reading, searching and editing files.
- todo_write / todo_read: for multi-step tasks, write a short plan first and update it as you go.

Guidelines:
- Use the tools to inspect files instead of guessing their contents; search before editing code you have not seen.
- Read-only tools run without approval. Writing, editing, moving, deleting files and running shell commands require the user's approval; if a call is denied, adapt instead of retrying the same call.
- Tool errors explain what went wrong and what to try next; follow the hint rather than repeating the call.
- Keep answers concise. Reply in the same language the user writes in.`, workspace, runtime.GOOS, runtime.GOARCH, shellNote(), builtinTrash)
}

// shellNote 告诉模型 shell 工具实际用的是什么 shell。
//
// 为什么要写在提示词里：模型不知道自己在 Windows 的 cmd.exe 里时，会在 bash、PowerShell、cmd 三种语法之间乱试，
// 大量步数耗在失败的命令上（真实会话里一次任务 40% 的调用失败）。每次调用是新进程这一点也要说清楚，
// 否则它会以为 cd 能保留，反复怀疑“是不是目录不对”。
func shellNote() string {
	if runtime.GOOS == "windows" {
		return "Shell: the shell tool runs commands with Windows cmd.exe (not bash, not PowerShell): use dir/type/findstr/where and chain with & or &&; for PowerShell write powershell -NoProfile -Command ... explicitly. Every call is a fresh process, so cd does not persist: use the cwd parameter instead."
	}
	return "Shell: the shell tool runs commands with sh -c. Every call is a fresh process, so cd does not persist: use the cwd parameter instead."
}

// builtinTrash 与 builtin.TrashDir 相同。内核不依赖 builtin 包（见 builtin/doc.go），所以这里重复一份。
const builtinTrash = ".myllmapp/trash/"

// ProjectContextFile 是工作区里的项目说明文件（相对工作区）。
const ProjectContextFile = ".myllmapp/context.md"

// maxProjectContext 是注入的项目说明的上限：它每轮都会发送，太长会挤占上下文。
const maxProjectContext = 16 * 1024

// ProjectContext 读取工作区的 .myllmapp/context.md，返回追加到系统提示词后面的一段；没有文件时返回空串。
//
// 用途：让用户写下项目的约定（技术栈、怎么跑测试、代码风格），不必每个会话都重复交代。
// 每轮都重新读取：用户改了文件，下一轮立即生效。内容稳定时不影响提示缓存。
func ProjectContext(workspace string) string {
	if workspace == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(ProjectContextFile)))
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	if len(text) > maxProjectContext {
		cut := maxProjectContext
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "\n...[truncated]"
	}
	return "\n\nProject context (from " + ProjectContextFile + ", written by the user):\n" + text
}
