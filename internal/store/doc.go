// Package store 实现会话持久化，即 core.SessionStore 接口。
//
// # 负责什么
//
//   - SQLite 实现（sqlite.go）：使用纯 Go 驱动 modernc.org/sqlite，不需要 CGO；
//     两张表：sessions（元数据、累计用量）和 messages（按序号保存中立格式消息的 JSON）。
//   - 内存实现（memory.go）：测试和"不想落盘"的 CLI 会话使用。
//   - 用量账本（usage.go）：usage_events 表（迁移 3），实现 usage.Source / usage.Sink，
//     聚合在 SQL 里完成；内存实现复用 usage.Aggregate，两者用同一组用例保证结果一致。
//     账本不设外键到 sessions：删除会话或清空历史不会抹掉用量统计。
//
// # 不负责什么
//
//   - 不决定何时保存：由 core.Engine 在每轮结束后调用 SaveSession。
//   - 不理解消息内容：消息以 provider.Message 的 JSON 原样存储，
//     所以给消息模型新增字段不需要数据库迁移。
//
// # 在架构中的位置
//
// 适配层。依赖 core（实现它定义的接口、使用 core.Session）和 provider（消息结构）。
// 内核不依赖本包——这是"依赖倒置"：接口在内核，实现在外层，由 internal/bootstrap 把两者接起来。
package store
