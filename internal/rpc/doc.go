// Package rpc 是 Go 后端和桌面壳（Electron 主进程）之间的通信：stdio 上一行一条的 JSON-RPC 2.0。
//
// # 负责什么
//
//   - peer.go：对等的一端——处理对方发来的请求和通知，也能向对方发请求（反向请求，例如“请弹一个选择文件夹的对话框”）
//     和通知（例如推一条 agent 事件）。每个请求在独立的 goroutine 里处理，回应靠 id 配对；
//   - dispatch.go：用反射把一个对象（*app.App）的公开方法变成按名字调用的表，参数是按位置的数组，
//     约定和 Wails 的绑定一致，前端不用为 Electron 版改调用方式。
//
// # 不负责什么
//
// 不知道有哪些业务方法、有哪些事件、壳要做什么——那是 internal/app 和 cmd/mosaicd 的事；本包只依赖标准库。
//
// # 为什么用 stdio 而不是本地 HTTP / WebSocket
//
// 不开端口，其他本机进程和网页就连不上后端，不需要另做鉴权；子进程的生命周期也天然跟着父进程：
// 父进程退出后 stdin 读到 EOF，后端随之退出。协议细节见 docs/design/electron-migration.md。
package rpc
