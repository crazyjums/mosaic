// Package codexquota 读取 ChatGPT 套餐的剩余额度（5 小时窗口、每周窗口），显示在状态栏上。
//
// # 为什么需要单独一个包、为什么是可选的
//
// "Sign in with ChatGPT"（internal/siwc）拿到的令牌目前**不能**查询剩余额度：OpenAI 官方答复暂不支持，
// ChatGPT / Codex 自己用的用量接口（chatgpt.com/backend-api/wham/usage）会拒绝这种令牌。
// 能查的只有 Codex 登录（官方 Codex CLI 的 `codex login`）的令牌。
//
// 所以这里的做法是：用户在设置里**主动开启**后，读取本机 Codex CLI 保存的登录信息（~/.codex/auth.json，
// 或 CODEX_HOME 指定的目录），只读地调用这个用量接口。约束：
//   - 不刷新、不修改 Codex 的令牌（刷新令牌会轮换，替 Codex 刷新会让 Codex 自己的登录失效）；
//     访问令牌过期就提示用户运行一次 codex；
//   - 令牌只在内存里用于这一个请求，不保存、不发给前端；
//   - 这是未公开的接口，字段可能变化，解析时尽量宽松，出错就显示原因，不影响对话。
//
// # 在架构中的位置
//
// 适配层，只依赖标准库；由 internal/settings 按设置调用。
package codexquota
