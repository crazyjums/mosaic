// Package app 是 Wails 绑定层：把 core.Engine 的命令暴露给前端，把事件总线转发成 Wails 事件。
//
// # 负责什么
//
//   - App 结构体的公开方法会被 Wails 绑定到前端，前端以 window.go.app.App.<方法名>(...) 调用；
//   - Startup 时订阅事件总线，每个事件通过 runtime.EventsEmit("agent:event", ev) 推给前端；
//   - Send 在 goroutine 中运行，避免阻塞 Wails 的方法调用。
//
// # 不负责什么
//
//   - 不写业务逻辑：会话管理、模型切换、权限等待全部在 core.Engine 里。
//     本包的每个方法基本都只有一两行转发——如果你发现这里的代码变多了，
//     多半说明有逻辑应该下沉到 core。
//
// # 与其他包的关系
//
// 入口层。依赖 core（Engine）、event（事件类型）和 Wails runtime。
// Wails runtime 的调用被收敛到 emit 函数里，测试时可以替换，所以本包不需要真正的 Wails 环境也能测试。
package app
