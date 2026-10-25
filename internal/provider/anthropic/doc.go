// Package anthropic 实现 Anthropic Messages API 的原生流式协议（POST /v1/messages, stream=true）。
//
// # 负责什么
//
//   - 把中立消息模型转换成 Messages API 请求（convert.go）。中立模型本身就是"内容块"结构，
//     所以转换几乎是一一对应的。
//   - 解析 SSE 事件流（anthropic.go）：
//     message_start（输入用量）→ content_block_start / content_block_delta / content_block_stop（各内容块）
//     → message_delta（停止原因、输出用量）→ message_stop。
//
// # 思考块（为什么要用原生协议）
//
//   - 请求默认开启 adaptive thinking，并设置 display = "summarized"，否则新模型默认不返回思考文本（只有空的思考块）。
//   - 每个 thinking 块结束时会收到 signature_delta；签名随消息存进历史，下次请求原样回传——
//     缺少签名或改动了思考内容，API 会拒绝请求。OpenAI 兼容层做不到这一点，这是保留原生适配器的主要原因。
//   - redacted_thinking 块（加密思考）同样原样保存和回传，界面只显示"已加密"。
//
// # 不负责什么
//
// 不使用官方 SDK：本项目的目标之一就是看清协议细节。代价是新特性需要手动跟进字段。
//
// # 在架构中的位置
//
// 适配层，实现 provider.Provider，在 init() 中注册为 "anthropic" 类型。
package anthropic
