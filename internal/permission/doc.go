// Package permission 负责"工具调用前要不要问用户、怎么等用户回答"。
//
// # 负责什么
//
//   - Risk：工具的风险等级（read / write / exec），由工具自己声明。
//   - Policy：根据风险等级和用户之前的选择，给出 Allow / Ask / Deny 的裁决。
//   - Broker：把"等用户回答"这件事从 UI 中抽离出来——内核调用 Request 阻塞等待，
//     界面（Wails 前端或 CLI）调用 Resolve 唤醒它。
//
// # 不负责什么
//
//   - 不发事件、不画弹窗。Broker.Request 接收一个 announce 回调，由内核在回调里发
//     permission_request 事件；本包因此不依赖 event 包，可以单独测试。
//   - 不执行工具。
//
// # 与其他包的关系
//
//	tool       ──引用──▶ permission.Risk（声明风险）
//	core       ──调用──▶ Policy.Evaluate / Broker.Request（执行工具前）
//	core.Engine ─调用──▶ Broker.Resolve（前端/CLI 回复时）
//
// 为什么把"策略"和"等待"拆成两个东西：策略是纯函数式的判断，容易换实现（例如以后支持
// 按路径/命令前缀配置规则）；等待涉及并发和取消，单独封装后内核只需一行调用。
package permission
