// Package archtest 只包含测试：对整个仓库做"架构与卫生"检查，把验收标准变成自动化的守卫。
//
//   - 每个 Go 包都有 doc.go（包注释是本项目可读性的基础）；
//   - 仓库中没有疑似真实密钥的字符串；
//   - config.example.toml 能被正常解析。
//
// 内核导入边界的检查在 internal/core/arch_test.go（它需要读取 core 包自身的文件）。
// 本包不被任何代码导入。
package archtest
