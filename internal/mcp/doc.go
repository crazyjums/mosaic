// Package mcp 把 MCP（Model Context Protocol）server 的工具接入本项目的工具系统。
//
// # 组成
//
//   - client.go：JSON-RPC 2.0 消息与 MCP 握手（initialize → notifications/initialized），tools/list（自动翻页）、tools/call；
//   - stdio.go：启动本地子进程，按"每行一条 JSON"收发消息；保留 stderr 末尾用于报错；
//   - http.go：Streamable HTTP 传输（POST + JSON 或 SSE 回复，Mcp-Session-Id 会话头，关闭时 DELETE）；
//   - mcp.go：ServerConfig（保存在 settings.json）与 ToolAdapter——把一个 MCP 工具包装成 tool.Tool；
//   - manager.go：按配置在后台连接各个 server，把工具注册进 / 移出 tool.Registry，并提供连接状态给设置页。
//
// # 负责什么 / 不负责什么
//
// 负责"MCP 世界 → 本项目工具系统"的转换；不负责权限（MCP 工具与内置工具一样声明 Risk，
// 由 core 统一走权限确认，默认 exec 即每次询问），也不负责事件（工具调用事件由 core 照常发出）。
//
// 只实现了客户端的 tools 能力。resources、prompts、sampling、服务器推送流（GET）暂未实现：
// 对"让模型调用外部工具"这个目标来说 tools 就够了，其余能力需要界面上有对应的交互才有意义。
//
// # 为什么自己实现而不用 SDK
//
// 用到的协议面很小（三个方法 + 两种传输，几百行），自己实现可以看清每一步，也少一个快速变化的依赖。
//
// # 在架构中的位置
//
// 适配层，依赖 tool 和 permission 契约；内核完全不知道 MCP 的存在——MCP 工具对内核来说就是普通工具。
package mcp
