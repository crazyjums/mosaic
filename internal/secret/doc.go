// Package secret 把敏感信息（目前是厂商的 API Key）保存到操作系统的钥匙串：
// macOS 钥匙串、Windows 凭据管理器、Linux 的 Secret Service（GNOME Keyring / KWallet）。
//
// # 为什么需要
//
// settings.json 的权限是 0600，已经只有当前用户能读；但它仍是一个明文文件，会被备份工具、
// 同步盘、误操作的 `cat` 带走。钥匙串里的条目受系统登录密码保护，也不会跟着数据目录被复制。
//
// # 为什么是可选的
//
// Linux 上不一定有 Secret Service（无桌面环境的服务器、精简发行版），而且钥匙串条目不会随数据目录迁移。
// 所以默认关闭，在"设置 → 常规"里开启；钥匙串不可用时界面上会说明原因并禁用开关。
//
// # 在架构中的位置
//
// 适配层，只被 settings 使用。接口很小（Get / Set / Delete），测试用内存实现。
package secret
