// Package mcp 是 MCP（Model Context Protocol）接入的预留扩展点。
//
// # 第一阶段的状态
//
//   - 已实现：ToolAdapter——把"某个 MCP server 上的一个工具"包装成 tool.Tool，
//     以及 RegisterServerTools——把一个 server 的全部工具注册进 tool.Registry。
//     这部分有单元测试（用假的 Client），证明 MCP 工具可以零改动地接入内核。
//   - 未实现：Client 接口的真实实现（stdio / Streamable HTTP 传输 + JSON-RPC 2.0 握手）。
//
// # 负责什么 / 不负责什么
//
// 负责"MCP 世界 → 本项目工具系统"的转换；不负责权限（MCP 工具与内置工具一样声明 Risk，
// 由 core 统一走权限确认），也不负责事件（工具调用事件由 core 照常发出）。
//
// # 后续接入步骤（详见 docs/roadmap.md）
//
//  1. 实现 Client：启动子进程（stdio）或连接 URL，完成 initialize 握手，实现 tools/list、tools/call；
//  2. 在配置中增加 [[mcp_servers]]，由 internal/bootstrap 为每个 server 创建 Client 并调用 RegisterServerTools；
//  3. 工具名加上 server 前缀（mcp__<server>__<tool>）避免与内置工具冲突——ToolAdapter 已经这样做了。
//
// # 在架构中的位置
//
// 适配层，依赖 tool 和 permission 契约；内核完全不知道 MCP 的存在。
package mcp
