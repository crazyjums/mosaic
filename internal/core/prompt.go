package core

import (
	"fmt"
	"runtime"
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

Guidelines:
- Use the provided tools to inspect and change files instead of guessing their contents.
- Writing files and running shell commands require the user's approval; if a call is denied, adapt instead of retrying the same call.
- Keep answers concise. Reply in the same language the user writes in.`, workspace, runtime.GOOS, runtime.GOARCH)
}
