// Package core 是 agent harness 的内核：会话、agent 循环、上下文管理、对外的命令接口。
//
// # 负责什么
//
//   - Agent（agent.go）：一次用户输入 → 多步"调用模型 → 执行工具 → 回传结果"的循环，直到模型不再调用工具
//     或达到最大步数。循环中的每一个可见变化都以 event.Event 发布。
//   - 累加器（accumulator.go）：把厂商吐出的 provider.Delta 组装成最终的 assistant 消息。
//   - 工具执行（tools.go）：权限裁决 → 等待确认 → 执行 → 结果，全部在这里串起来。
//   - Engine（engine.go）：内核的"命令接口"。CLI 和 Wails 绑定层只调用 Engine 的方法、只订阅 Engine 的事件总线。
//   - 会话（session.go）与重放（replay.go）：会话数据结构、持久化接口、把历史消息重放成事件。
//   - 预留扩展点（只有接口）：Compactor（上下文压缩）、子 agent 定义与调度、后台任务。
//
// # 不负责什么
//
//   - 不认识任何具体厂商：只依赖 provider.Provider 接口。
//   - 不认识任何界面：不导入 Wails，不打印终端，只发事件。
//   - 不实现持久化：只定义 SessionStore 接口，由 internal/store 实现（依赖倒置）。
//
// 这三条由 arch_test.go 自动检查。
//
// # 与其他包的关系
//
//	core ──依赖──▶ event / provider（接口）/ tool（接口）/ permission
//	store、bootstrap、app、cmd/cli ──依赖──▶ core
//
// 阅读建议：先读 agent.go 的 Run（主循环），再读 tools.go（权限等待），最后读 engine.go（命令接口）。
package core
