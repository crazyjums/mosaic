// Package hooks 运行钩子：在 agent 循环的固定位置（工具调用前后、用户发言时、一轮结束时）执行
// 用户批准过的外部命令。格式和 Claude Code 的 hooks 兼容（事件名、matcher、标准输入的 JSON、
// 退出码 0 / 2、标准输出的 JSON），这样现成的 Claude Code 插件里的钩子大多能直接用。
//
// 为什么单独一个包：内核（internal/core）只定义“在哪里问、怎么用答案”（core.Hooks），
// 不该知道怎么起进程、怎么匹配、怎么限时。这个包实现 core.Hooks，由装配层注入。
//
// 和 Claude Code 不同、更保守的地方（详见 docs/design/hooks-and-subagents.md §2.2）：
//   - permissionDecision: "allow" 不跳过确认（只当作没表态）；
//   - 默认时限 30 秒（UserPromptSubmit 10 秒），最多 120 秒；输出最多 10000 字；
//   - 只拿白名单环境变量（和插件的 MCP 服务一样，不含 API Key）；
//   - 同一事件的多条钩子按顺序跑（不是并行），前一条改过的参数后一条能看到；PreToolUse 一旦被阻止就不再跑后面的。
//
// 第一版只支持 command 类型；钩子只来自插件（H2 接上逐条批准）。
package hooks
