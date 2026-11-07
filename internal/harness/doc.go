// Package harness 定义“外部 agent 引擎”的契约。
//
// 为什么需要它：Mosaic 内置的 agent 循环（internal/core 的 Agent）自己调用模型、自己执行工具。
// Codex、Claude Code 这类产品把“循环 + 工具 + 沙箱”做在了自己里面——我们不能（也不该）再套一层自己的循环，
// 只能把它当作一个黑盒引擎来驱动：把用户的话交给它，把它产生的过程翻译成 Mosaic 的统一事件，
// 把它的审批请求转成 Mosaic 的权限确认。界面、会话历史、权限弹窗都因此保持原样。
//
// 本包只放契约（接口和数据类型），不含任何具体引擎，和 internal/provider 的角色一样：
// 内核只依赖这里，具体实现在子包里（例如 internal/harness/codex），由装配层（bootstrap）注册。
package harness
