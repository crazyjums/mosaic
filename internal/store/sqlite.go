package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/provider"

	_ "modernc.org/sqlite" // 注册名为 "sqlite" 的 database/sql 驱动
)

// FileName 是会话数据库在数据目录下的文件名。
const FileName = "sessions.db"

// SQLite 是基于 SQLite 文件的会话存储。
type SQLite struct {
	db *sql.DB
}

// 编译期检查：确保 SQLite 实现了内核需要的接口。接口一改，这里立刻编译失败。
var _ core.SessionStore = (*SQLite)(nil)

// migrations 按顺序执行，PRAGMA user_version 记录已执行到第几条。
// 只追加、不修改已有条目——这是最简单可靠的迁移方式，不需要引入迁移框架。
var migrations = []string{
	`CREATE TABLE sessions (
		id         TEXT PRIMARY KEY,
		title      TEXT NOT NULL DEFAULT '',
		provider   TEXT NOT NULL DEFAULT '',
		model      TEXT NOT NULL DEFAULT '',
		usage_json TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE messages (
		session_id   TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		seq          INTEGER NOT NULL,
		role         TEXT    NOT NULL,
		content_json TEXT    NOT NULL,
		PRIMARY KEY (session_id, seq)
	);
	CREATE INDEX idx_sessions_updated ON sessions(updated_at DESC);`,
	// 2：每个会话可以有自己的工作区（为空表示跟随默认工作区）。
	`ALTER TABLE sessions ADD COLUMN workspace TEXT NOT NULL DEFAULT '';`,
}

// OpenSQLite 打开（必要时创建）数据库文件并执行迁移。
func OpenSQLite(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	// WAL 让读写互不阻塞；busy_timeout 避免偶发的 "database is locked"；foreign_keys 让级联删除生效。
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 只允许一个写者；限制为单连接最简单，也足够桌面应用使用。
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration %d: %w", i+1, err)
		}
		// PRAGMA 不支持参数绑定，这里的数字来自代码而不是用户输入，拼接是安全的。
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) Close() error { return s.db.Close() }

// SaveSession 保存整个会话：更新元数据，并整体重写消息。
//
// 取舍：整体重写 vs. 只追加新消息。追加更省 I/O，但上下文压缩会"改写"历史，
// 追加模型就要处理删除和重排；整体重写在一个事务里完成，逻辑简单且总是正确。
// 单个会话几百条消息的量级下，重写的成本可以忽略。
func (s *SQLite) SaveSession(ctx context.Context, sess *core.Session) error {
	usage, err := json.Marshal(sess.Usage)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit 之后再 Rollback 是无害的空操作

	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions (id, title, provider, model, workspace, usage_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title, provider = excluded.provider, model = excluded.model,
			workspace = excluded.workspace, usage_json = excluded.usage_json, updated_at = excluded.updated_at`,
		sess.ID, sess.Title, sess.Provider, sess.Model, sess.Workspace, string(usage),
		sess.CreatedAt.UnixMilli(), sess.UpdatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("store: upsert session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, sess.ID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO messages (session_id, seq, role, content_json) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, m := range sess.Messages {
		content, err := json.Marshal(m.Content)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, sess.ID, i, string(m.Role), string(content)); err != nil {
			return fmt.Errorf("store: insert message %d: %w", i, err)
		}
	}
	return tx.Commit()
}

func (s *SQLite) LoadSession(ctx context.Context, id string) (*core.Session, error) {
	var (
		sess             core.Session
		usage            string
		created, updated int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, provider, model, workspace, usage_json, created_at, updated_at FROM sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.Title, &sess.Provider, &sess.Model, &sess.Workspace, &usage, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", core.ErrSessionNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(usage), &sess.Usage); err != nil {
		return nil, fmt.Errorf("store: decode usage: %w", err)
	}
	sess.CreatedAt, sess.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)

	rows, err := s.db.QueryContext(ctx, `SELECT role, content_json FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return nil, err
		}
		m := provider.Message{Role: provider.Role(role)}
		if err := json.Unmarshal([]byte(content), &m.Content); err != nil {
			return nil, fmt.Errorf("store: decode message: %w", err)
		}
		sess.Messages = append(sess.Messages, m)
	}
	return &sess, rows.Err()
}

func (s *SQLite) ListSessions(ctx context.Context) ([]core.SessionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.title, s.provider, s.model, s.workspace, s.created_at, s.updated_at,
		       (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id)
		FROM sessions s ORDER BY s.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.SessionSummary
	for rows.Next() {
		var (
			x                core.SessionSummary
			created, updated int64
		)
		if err := rows.Scan(&x.ID, &x.Title, &x.Provider, &x.Model, &x.Workspace, &created, &updated, &x.MessageCount); err != nil {
			return nil, err
		}
		x.CreatedAt, x.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// Compact 回收已删除数据占用的磁盘空间（清空历史会话后调用）。
// SQLite 删除数据后文件不会自动变小：先把 WAL 合并回主文件，再 VACUUM 重建数据库。
func (s *SQLite) Compact(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `VACUUM`)
	return err
}
