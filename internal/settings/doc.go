// Package settings 管理"在界面上配置的厂商"：持久化、与配置文件合并、修改后立即生效。
//
// # 负责什么
//
//   - 设置文件（file.go）：{data_dir}/settings.json，保存界面上添加/修改的厂商配置档
//     （Base URL、API Key、模型列表……）以及上次选择的厂商和模型。文件权限 0600。
//   - 合并（merge.go）：config.toml 里的配置档 + 设置文件里的配置档 → 最终的配置档列表。
//     同名时设置文件覆盖配置文件（界面上的修改优先）。
//   - Manager（manager.go）：给界面用的增删改查、测试连接、获取模型列表、记住上次选择；
//     每次修改后重建 provider.Catalog 并调用 core.Engine.ReplaceCatalog，不需要重启。
//
// # 不负责什么
//
//   - 不解析 config.toml（internal/config 的事），也不修改它——界面上的修改只写设置文件，
//     这样手写的 config.toml（含注释）永远不会被程序改写。
//   - 不实现任何厂商协议，只通过 provider.Registry 创建实例。
//
// # 为什么单独一个文件、而不是写回 config.toml
//
// 考虑过直接改写 config.toml：TOML 库写回时会丢掉注释和格式，用户手写的说明会消失；
// 而且配置文件可能被放进版本库共享，不适合存放界面上填写的密钥。
// 所以分成两层：config.toml 是"手写的、可共享的"，settings.json 是"程序管理的、本机私有的"。
//
// # 密钥存储
//
// 第一阶段 API Key 以明文保存在 settings.json（权限 0600，位于用户主目录下，不在仓库里）。
// 更安全的做法是存进系统钥匙串（macOS Keychain / Windows Credential Manager / Secret Service），
// 见 docs/roadmap.md。发给前端的视图里密钥只有掩码提示，不含明文。
//
// # 在架构中的位置
//
// 装配层，与 bootstrap 同级：依赖 core（Engine）、provider（注册表与目录）。
// 被 bootstrap（启动时合并）、internal/app（界面调用）、cmd/cli（记住选择）使用。
package settings
