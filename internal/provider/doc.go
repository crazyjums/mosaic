// Package provider 定义"模型厂商"的抽象：厂商中立的消息模型、Provider 接口、厂商注册表。
//
// # 负责什么
//
//   - 中立消息模型（message.go）：Message / Block。会话历史就存这个结构，与具体厂商无关。
//   - 调用契约（provider.go）：Request（发什么）、Delta（流式收到什么）、Provider（怎么调用）。
//   - 注册与实例化（registry.go）：适配器类型（openai / anthropic / fake）→ 工厂函数；
//     配置里的每个"厂商配置档"（profile）→ 一个 Provider 实例，汇总在 Catalog 里。
//
// # 不负责什么
//
//   - 不实现任何具体协议：OpenAI 兼容协议在 provider/openai，Anthropic 原生协议在 provider/anthropic，
//     测试用的脚本厂商在 provider/fake。
//   - 不组装最终消息：厂商只吐 Delta，组装由 internal/core 的累加器完成（原因见 Provider 注释）。
//   - 不读配置文件：配置解析在 internal/config，这里只接收已经解析好的 ProfileConfig。
//
// # 在架构中的位置
//
// 契约层。internal/core 依赖本包的接口；各厂商子包实现本包的接口；
// 本包不依赖项目内任何其他包，因此 core 永远不需要知道有哪些厂商。
//
// 新增厂商：新建子包 → 实现 Provider → 在 init() 里调用 Register → 在 provider/all 里加一行空导入。
// 详见 docs/extending.md。
package provider
