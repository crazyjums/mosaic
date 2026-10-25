// Package tool 定义工具系统：Tool 接口和工具注册表。
//
// # 负责什么
//
//   - Tool 接口：一个工具 = 规格（名称、描述、参数 JSON Schema、风险等级）+ 执行函数。
//   - Registry：按名称注册和查找工具，并把所有工具规格导出给模型。
//
// # 不负责什么
//
//   - 不实现具体工具：内置工具在 tool/builtin，每个工具一个文件。
//   - 不做权限判断：工具只"声明"自己的风险等级（permission.Risk），
//     是否需要用户确认由 internal/core 调用 permission.Policy 决定。
//     这样工具作者不需要关心 UI，权限策略也能统一修改。
//   - 不知道模型协议：工具规格转换成 provider.ToolSpec 由 core 完成。
//
// # 在架构中的位置
//
// 契约层。依赖 permission（仅 Risk 类型）；被 core 和 tool/builtin 依赖。
// 未来的 MCP 工具（见 internal/mcp）会被包装成本包的 Tool 后注册进 Registry，
// 对内核来说与内置工具没有区别。
package tool
