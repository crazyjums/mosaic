// Package bootstrap 是"装配车间"：根据配置把各层组件组装成一个可用的 core.Engine。
//
// # 负责什么
//
//   - 解析工作区和数据目录路径；
//   - 创建工具注册表并注册内置工具；
//   - 合并 config.toml 与界面设置（internal/settings 的 settings.json）中的厂商配置档，并选出启动时使用的厂商；
//   - 打开 SQLite 会话存储；
//   - 选择权限策略、生成默认系统提示词；
//   - 最后创建 core.Engine。
//
// # 不负责什么
//
//   - 不包含业务逻辑，只是"new 一堆对象并连起来"。
//
// # 为什么单独成包
//
// CLI（cmd/cli）和桌面端（internal/app + main.go）需要完全相同的组装过程。放在这里，
// 两个入口各自只剩"解析参数 → bootstrap.Build → 接上自己的界面"三步，保证两者行为一致。
//
// 在架构中，本包是唯一一个"知道所有具体实现"的地方（依赖注入的组合根，composition root）。
package bootstrap
