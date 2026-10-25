// Package builtin 提供第一阶段的四个内置工具：read_file、write_file、list_dir、shell。
//
// # 负责什么
//
//   - 每个工具一个文件，彼此独立，都实现 tool.Tool。
//   - 公共的安全辅助函数（workspace.go）：把相对路径解析到工作区内，拒绝逃逸；截断过长输出。
//
// # 不负责什么
//
//   - 不做权限确认：每个工具只在 Spec 里声明风险等级（read / write / exec），
//     确认流程由 internal/core + internal/permission 负责。
//
// # 与其他包的关系
//
// 依赖 tool 和 permission（契约层），由 internal/bootstrap 调用 Register 注册到工具注册表。
// 内核不直接引用本包——对内核来说，内置工具和将来的 MCP 工具没有区别。
//
// 安全说明：工作区限制只是"防误操作"，不是安全沙箱。shell 工具可以执行任意命令，
// 这正是它被标记为 exec 风险、默认每次都需要确认的原因。
package builtin
