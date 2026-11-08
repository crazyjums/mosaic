// Package agents 读取子 agent 的定义文件，翻译成 core.SubagentDefinition。
//
// # 负责什么
//
//   - 解析 Claude Code 格式的 agent 文件（Markdown，开头 --- 包起来的 frontmatter，正文是系统提示词）：
//     name、description（必填）、tools、disallowedTools、model、maxTurns；
//   - 把 Claude Code 的工具名（Read、Grep、Bash……）映射成 Mosaic 的内置工具名（read_file、search_content、shell……），
//     对不上的记成 Warnings 让界面显示，而不是悄悄丢掉；
//   - 扫描一个目录（用户的 ~/.myllmapp/agents/、插件的 agents/）。
//
// # 不负责什么
//
//   - 不运行子 agent：调度在 internal/core（task 工具）；
//   - 不读 permissionMode、hooks、mcpServers：子 agent 和主对话用同一套权限，不能通过定义文件给自己放宽权限
//     或带进可执行组件（Claude Code 对插件 agent 也是这么做的）。出现这些字段时记一条 Warning。
//
// 为什么不引入 YAML 库：frontmatter 里只有几个简单字段（字符串、逗号分隔或 “- 项” 的列表、数字），
// 和 internal/skill 一样手写一个够用的解析器，少一个依赖，也少一类解析器漏洞。
package agents
