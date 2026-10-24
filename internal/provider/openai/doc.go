// Package openai 实现 OpenAI 兼容的 Chat Completions 流式协议。
//
// # 负责什么
//
//   - 把中立消息模型转换成 Chat Completions 请求（convert.go）；
//   - 解析 SSE 流，翻译成 provider.Delta（openai.go）：
//     文本 → DeltaText；reasoning_content / reasoning → DeltaThinking；
//     按 index 分片到达的 tool_calls → 拼接完整后一次性发 DeltaToolCall；
//     usage 和 finish_reason → DeltaUsage / DeltaStop。
//
// # 覆盖哪些厂商
//
// 所有提供 OpenAI 兼容接口的厂商：OpenAI、DeepSeek、通义千问（DashScope 兼容模式）、Moonshot、
// 智谱、硅基流动、OpenRouter、本地 Ollama / vLLM 等。区别只在 base_url、密钥和模型名，写在配置里即可。
//
// # 降级行为
//
//   - 思考：只有部分厂商在 delta 里返回 reasoning_content（DeepSeek、Qwen 等）或 reasoning（OpenRouter 等）。
//     都没有时不产生思考 Delta，界面自然不显示思考卡片。
//   - 历史中的思考块：发送请求时丢弃。OpenAI 协议没有"回传思考"的标准字段。
//
// # 在架构中的位置
//
// 适配层，实现 provider.Provider，在 init() 中注册为 "openai" 类型。不被内核直接引用。
package openai
