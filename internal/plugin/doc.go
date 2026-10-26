// Package plugin 是插件系统：从磁盘加载插件包、安装确认、启停、配置，并把插件带来的东西分发给已有的注册表。
//
// 方案和取舍见 docs/design/plugin-system.md。
//
// # 负责什么
//
//   - manifest.go：plugin.json 的格式与校验（路径必须在插件目录内、变量必须是已知的、数量和长度有上限、
//     暂不支持的字段明确拒绝）；
//   - host.go：Host——扫描已安装的插件，Preview / Install / SetEnabled / Uninstall / SetConfig，
//     以及 SkillFolders、Commands、ExpandCommand、Status、RunAction；
//   - catalog.go：插件市场（内置插件、在线列表及其本地缓存）和从 Git 仓库取插件；列表只提供来源，装之前仍走 Preview 确认；
//   - archive.go：zip 解压和目录复制，防 zip slip、符号链接、压缩炸弹；
//   - vars.go / run.go：命令行拆分与变量展开（先拆后展开，不经过 shell），状态检查和操作按钮的运行；
//   - command.go：斜杠命令的提示词模板；
//   - plugin.go / statusline.go：钩子的接口（预留）和状态栏脚本。
//
// # 不负责什么
//
//   - 不运行 MCP 服务：它只把插件声明的服务展开成 mcp.ServerConfig，交给 mcp.Manager.ApplySource；
//   - 不发现技能：它只提供技能文件夹（skill.Folder），发现和读取在 internal/skill；
//   - 不做权限裁决：插件的工具照常走 permission；清单里声明的 read 风险只有在安装确认时用户勾选后才生效；
//   - 不渲染界面：设置页的表单、状态点、按钮由前端根据 Info 渲染，插件不能注入前端代码；
//   - 钩子（hooks）这个版本还没有实现：清单里出现 hooks 会被拒绝。
//
// # 安全
//
// 插件是用户电脑上的外部程序，无法沙箱。能做到的是：安装前让用户看清楚要运行什么并确认；
// 子进程只拿白名单环境变量（mcp.BaseEnv）；secret 配置只存系统钥匙串、不返回给界面；
// 路径逃逸、zip 里的恶意条目、符号链接一律拒绝。
//
// # 在架构中的位置
//
// 适配层的包：由 internal/bootstrap 创建，被 internal/app 调用；internal/core 不依赖它。
package plugin
