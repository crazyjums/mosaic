// Package siwc 实现 OpenAI 的"Sign in with ChatGPT"（开源应用版）：用户用 ChatGPT 账号登录后，
// 本应用拿到一个 OAuth 访问令牌，可以用 ChatGPT 套餐里的额度调用 Responses API。
//
// # 流程
//
//  1. 第一次登录用 client_id=dynamic_agent_client 加上 agent_name_hint（应用名）发起授权，
//     这是"动态注册"的入口；用户在浏览器里确认后，回调带回 OpenAI 为本应用签发的 client_id（oaiapp_…），
//     之后同一账号再登录就直接用它，不再注册；
//  2. 标准的 OAuth 授权码 + PKCE：本地在 127.0.0.1 的随机端口上开一个只接受一次请求的回调服务器；
//  3. 用授权码换令牌，校验 ID 令牌（RS256 签名、iss、aud、exp、nonce）；
//  4. 访问令牌 1 小时过期，刷新令牌 30 天、每次刷新都会换新（旧的立即作废），所以刷新必须串行，
//     并且跨进程（桌面端和 CLI 可能同时运行）也要加锁，见 store.go；
//  5. 退出登录时吊销刷新令牌、删除本地令牌，但保留签发的 client_id，下次登录不用重新注册。
//
// 每台电脑有一个固定的 ext_agent_host_id（urn:uuid:…），首次使用时生成。
//
// # 条款要点（OpenAI 的 Sign in with ChatGPT 条款）
//
// 登录时使用本应用自己的名字；令牌只存放在用户本机；请求只能由用户在本机发起；
// 套餐额度只能给本应用使用，不能转给其他工具或其他人。所以令牌不会通过任何接口发给前端或外部。
//
// 为什么不复用 Codex CLI 的 client_id：那是 Codex 自己的客户端，第三方冒用违反条款，
// 而且要配合 chatgpt.com 的内部接口才能用，随时可能失效。动态注册是 OpenAI 给开源应用的正式途径。
//
// # 在架构中的位置
//
// 适配层，只依赖标准库。它实现 provider.TokenSource 的方法（Token），由 internal/settings
// 注入给 chatgpt 适配器；它自己不知道 provider 包的存在。
package siwc
