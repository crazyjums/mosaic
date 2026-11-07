// Package remote 让“官方 SDK 是 Node 包”的引擎（Claude Code、pi）接进 Mosaic 的 harness 契约。
//
// 这些 SDK 不能被 Go 直接调用。做法：SDK 跑在 Electron 的 utilityProcess 里（由壳管理），Go 这边只是一个很薄的
// harness.Harness 实现，把一轮对话变成发给壳的一个反向请求，壳再把引擎过程中的事件和审批请求送回来：
//
//	Go（Run）──host.harness.run──▶ 壳 ──▶ utilityProcess（SDK）
//	Go ◀──harness.emit / harness.ask── 壳 ◀── utilityProcess
//	Go（ctx 取消）──host.harness.cancel──▶ 壳
//
// 所以这些引擎只在 Electron 版里可用；Wails 版和命令行里它们对应的厂商配置档会给出明确的提示，而不是悄悄不工作。
//
// 安全：壳只会把自己的 utilityProcess 产生的东西送进来，但这里仍然校验——只接受已登记的 runID，
// 只接受引擎有权发的事件类型（不能伪造 turn_end、permission_* 这些由内核掌握的事件）。
package remote
