// Command mosaicd 是 Electron 版桌面端的 Go 后端：和 Wails 版用同一套内核和同一个 internal/app，
// 区别只在“界面”——它不开窗口，而是在 stdin / stdout 上用 JSON-RPC（internal/rpc）服务 app.App 的全部公开方法，
// 由 Electron 主进程作为子进程拉起。
//
// 用法（通常由 Electron 主进程调用，开发时也可以手动运行）：
//
//	mosaicd [-config 路径] [-workspace 目录] [-changelog CHANGELOG.md] [-demo]
//
// stdout 只写协议消息（程序启动时把 os.Stdout 换成了 stderr，任何意外的打印都不会破坏协议）；日志写 stderr。
// stdin 读到 EOF（父进程退出）时取消所有会话并退出。协议和迁移计划见 docs/design/electron-migration.md。
package main
