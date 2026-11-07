// Package app 是桌面端的绑定层：把 core.Engine 的命令暴露给前端，把事件总线转发给前端。
//
// # 负责什么
//
//   - App 结构体的公开方法就是前端能调用的接口：Wails 版里绑定成 window.go.app.App.<方法名>(...)，
//     Electron 版里由 cmd/mosaicd 通过 stdio JSON-RPC 按同样的方法名提供（internal/rpc）；
//   - Startup 时订阅事件总线，每个事件通过 Host.Emit("agent:event", ev) 推给前端；
//   - Send 在 goroutine 中运行，避免阻塞 Wails 的方法调用。
//
// # 不负责什么
//
//   - 不写业务逻辑：会话管理、模型切换、权限等待全部在 core.Engine 里，
//     厂商配置的增删改查、测试连接在 internal/settings 里。
//     本包的每个方法基本都只有一两行转发——如果你发现这里的代码变多了，
//     多半说明有逻辑应该下沉到 core。
//
// # 与其他包的关系
//
// 入口层。依赖 core（Engine）、settings（厂商配置）、event（事件类型）。
// 需要窗口的能力（推事件、打开链接、对话框、退出）都收敛在 Host 接口里（host.go），本包不依赖任何 GUI 框架：
// Wails 的实现在仓库根的 main 包，Electron 版的实现在 cmd/mosaicd。
package app
