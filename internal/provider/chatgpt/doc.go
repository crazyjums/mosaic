// Package chatgpt 是"用 ChatGPT 账号登录"（Sign in with ChatGPT）的厂商适配器：
// 用 OAuth 访问令牌调用 OpenAI 的 Responses API（https://api.openai.com/v1/responses），
// 消耗的是用户 ChatGPT 套餐里的额度，而不是 API 余额。
//
// # 与 openai 适配器的区别
//
//   - 协议是 Responses API，不是 Chat Completions：输入是"条目"（消息、函数调用、函数结果）列表，
//     系统提示词放在 instructions 字段，流式事件按语义命名（response.output_text.delta 等）；
//   - 认证用 provider.TokenSource（令牌一小时过期，由 internal/siwc 刷新），不用固定的 API Key；
//   - 套餐额度这条路有额外限制（OpenAI 的 Sign in with ChatGPT 文档）：必须 store=false、stream=true；
//     不能发 max_output_tokens、temperature 等字段；不支持 system 角色的消息条目。
//
// 为什么不复用 Codex CLI 的登录和 chatgpt.com 的内部接口：那是 Codex 自己的客户端和未公开的接口，
// 第三方冒用它的 client_id 和 originator 违反条款，也随时可能失效。OpenAI 为开源应用提供了正式的
// 动态注册流程（见 internal/siwc），我们走这条路。
//
// # 在架构中的位置
//
// 适配层，只依赖 provider 契约和 sse 解析器；登录、令牌存储与刷新都在 internal/siwc，这里一概不知。
package chatgpt
