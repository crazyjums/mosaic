# 设计：持久化（internal/store）

## 目标

会话可以保存和恢复；内核不依赖具体存储；不需要 CGO。

## 接口在哪

```go
// internal/core/session.go —— 由使用方（内核）定义
type SessionStore interface {
    SaveSession(ctx, *Session) error
    LoadSession(ctx, id) (*Session, error)
    ListSessions(ctx) ([]SessionSummary, error)
    DeleteSession(ctx, id) error
}
```

Go 的惯例是"接口由使用者定义"。这样内核不依赖 SQLite；`store` 包依赖 `core`（实现它的接口）——依赖方向是外层指向内层，符合分层规则。两个实现都有编译期断言 `var _ core.SessionStore = (*SQLite)(nil)`。

## 何时保存

`Engine.Send` 在每轮结束后调用 `SaveSession`，**包括出错和取消的轮**（用户已经看到的内容不应丢失）。保存用 `context.Background()`，避免本轮被取消时保存也被取消。保存失败发出非致命的 `error{code: store_error}` 事件。

## SQLite 实现

驱动：[`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite)——SQLite 的 C 代码被机器翻译成 Go，**不需要 CGO**，交叉编译和 CI 都简单。代价是比 `mattn/go-sqlite3` 慢一些、二进制更大，对聊天记录的量级没有影响。

连接参数：`foreign_keys(1)`（级联删除）、`journal_mode(WAL)`（读写不互相阻塞）、`busy_timeout(5000)`；`SetMaxOpenConns(1)`（SQLite 只允许一个写者，桌面应用单连接足够）。

### 表结构

```sql
CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL DEFAULT '',
    provider   TEXT NOT NULL DEFAULT '',   -- 配置档名称
    model      TEXT NOT NULL DEFAULT '',
    workspace  TEXT NOT NULL DEFAULT '',   -- 会话的工作区；空表示跟随默认工作区（迁移 2 新增）
    usage_json TEXT NOT NULL DEFAULT '{}', -- 累计用量
    created_at INTEGER NOT NULL,           -- Unix 毫秒
    updated_at INTEGER NOT NULL
);
CREATE TABLE messages (
    session_id   TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,
    role         TEXT    NOT NULL,
    content_json TEXT    NOT NULL,          -- []provider.Block 的 JSON
    PRIMARY KEY (session_id, seq)
);
CREATE INDEX idx_sessions_updated ON sessions(updated_at DESC);
```

- 消息内容以中立格式的 JSON 原样存储：给 `Block` 新增字段**不需要迁移**；用 `sqlite3` 命令行打开就能看懂。
- 数据库位置：`{data_dir}/sessions.db`，默认 `~/.myllmapp/sessions.db`。

### 迁移

`migrations` 是一个字符串切片，`PRAGMA user_version` 记录执行到第几条。规则：**只追加，不修改已有条目**。这是最简单可靠的迁移方式，不需要迁移框架。

| 版本 | 内容 |
| --- | --- |
| 1 | 建表 `sessions`、`messages` |
| 2 | `sessions` 增加 `workspace` 列（每个会话的工作区）。旧会话为空字符串，跟随默认工作区；`TestSQLiteUpgradeFromV1` 覆盖了从旧库升级 |

### 写入策略：整体重写

每次保存在一个事务里：upsert 会话行 → 删除该会话所有消息 → 重新插入。

| 方案 | 优点 | 缺点 |
| --- | --- | --- |
| **整体重写（当前）** | 逻辑简单，总是正确；上下文压缩"改写历史"时无需特殊处理 | 每轮写 O(n) 条消息 |
| 只追加新消息 | I/O 少 | 压缩改写历史时要处理删除和重排 |

单会话几百条消息时重写成本可以忽略。如果以后出现超长会话的性能问题，再改成"追加 + 压缩时整体重写"。

## 内存实现

`store.Memory`，用于测试和 CLI 的 `-no-store`。存取都做快照拷贝，模拟真实存储"存进去的是副本"的语义。

## 没有持久化的东西

事件本身不存储。会话恢复时由 `core.ReplayEvents` 从消息重建事件，因此工具耗时、权限确认过程、每条消息的用量不会显示。如果以后需要"完整回放"，可以加一张 `events` 表，由一个事件总线订阅者写入——不需要改内核。

## 测试

`store_test.go` 对两个实现跑同一组用例：保存/读取（含思考签名、工具调用）、覆盖保存、列表与消息计数、删除、SQLite 重新打开后迁移幂等。
