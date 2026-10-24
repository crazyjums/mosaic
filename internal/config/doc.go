// Package config 负责读取配置：配置文件（TOML）+ 环境变量。
//
// # 负责什么
//
//   - 按优先级查找配置文件：命令行指定 > 环境变量 MYLLMAPP_CONFIG > ./config.toml > ~/.myllmapp/config.toml；
//     都没有时使用内置默认配置（fake + 常见厂商，密钥从环境变量读）。
//   - 解析厂商配置档，把 api_key_env 指向的环境变量解析成明文密钥，交给 provider 包。
//
// # 不负责什么
//
//   - 不创建任何对象：把配置变成 Engine、工具、存储是 internal/bootstrap 的事。
//     这样本包可以被单独测试，也方便以后加一个"设置界面"直接读写 Config 结构。
//
// # 密钥安全
//
//   - 推荐在配置里只写 api_key_env（环境变量名），密钥本身不落盘；
//   - config.toml 已在 .gitignore 中，仓库只提交 config.example.toml；
//   - 解析出的明文密钥只存在于内存中的 provider.ProfileConfig，不会发给前端（见 provider.ProfileInfo）。
package config
