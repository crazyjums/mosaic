package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/provider"
)

func sampleSession(id string) *core.Session {
	s := core.NewSession(id, "fake", "demo")
	s.Title = "测试会话"
	s.Workspace = "/repos/demo"
	s.Usage = provider.Usage{InputTokens: 10, OutputTokens: 5}
	s.Messages = []provider.Message{
		provider.UserText("你好"),
		{Role: provider.RoleAssistant, Content: []provider.Block{
			{Type: provider.BlockThinking, Text: "想一想", Signature: "sig=="},
			{Type: provider.BlockText, Text: "我来看看"},
			{Type: provider.BlockToolUse, ToolCallID: "c1", ToolName: "list_dir", ToolInput: json.RawMessage(`{"path":"."}`)},
		}},
		{Role: provider.RoleUser, Content: []provider.Block{
			{Type: provider.BlockToolResult, ToolCallID: "c1", Text: "a/\nb.txt"},
		}},
	}
	return s
}

// 两种实现跑同一组用例，保证行为一致。
func testStore(t *testing.T, st core.SessionStore) {
	ctx := context.Background()
	s := sampleSession("sess_1")
	if err := st.SaveSession(ctx, s); err != nil {
		t.Fatal(err)
	}

	got, err := st.LoadSession(ctx, "sess_1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Messages, s.Messages) {
		t.Fatalf("消息不一致\n got  %+v\n want %+v", got.Messages, s.Messages)
	}
	if got.Title != s.Title || got.Usage != s.Usage || got.Provider != "fake" || got.Workspace != "/repos/demo" {
		t.Fatalf("元数据不一致: %+v", got)
	}

	// 再次保存（追加一条消息），应覆盖而不是重复。
	s.Messages = append(s.Messages, provider.Message{Role: provider.RoleAssistant, Content: []provider.Block{{Type: provider.BlockText, Text: "完成"}}})
	s.UpdatedAt = time.Now().Add(time.Second)
	if err := st.SaveSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSession(ctx, sampleSession("sess_2")); err != nil {
		t.Fatal(err)
	}

	list, err := st.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个会话: %+v", list)
	}
	for _, x := range list {
		if x.ID == "sess_1" && (x.MessageCount != 4 || x.Workspace != "/repos/demo") {
			t.Fatalf("sess_1 应有 4 条消息: %+v", x)
		}
	}

	if err := st.DeleteSession(ctx, "sess_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadSession(ctx, "sess_1"); !errors.Is(err, core.ErrSessionNotFound) {
		t.Fatalf("删除后应返回 ErrSessionNotFound: %v", err)
	}
}

func TestMemory(t *testing.T) { testStore(t, NewMemory()) }

func TestSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "data.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	testStore(t, st)
	st.Close()

	// 重新打开：迁移应幂等，数据应还在。
	st2, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, err := st2.LoadSession(context.Background(), "sess_2"); err != nil {
		t.Fatalf("重新打开后应能读到数据: %v", err)
	}
}

func TestSQLiteCompact(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_ = st.SaveSession(ctx, sampleSession("a"))
	_ = st.DeleteSession(ctx, "a")
	if err := st.Compact(ctx); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if list, _ := st.ListSessions(ctx); len(list) != 0 {
		t.Fatal("应为空")
	}
}

// 旧版本（只有第 1 条迁移）的数据库升级后，已有会话的工作区为空（跟随默认工作区）。
func TestSQLiteUpgradeFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{migrations[0], `PRAGMA user_version = 1`,
		`INSERT INTO sessions (id, title, created_at, updated_at) VALUES ('old', '旧会话', 1, 1)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.LoadSession(context.Background(), "old")
	if err != nil || got.Title != "旧会话" || got.Workspace != "" {
		t.Fatalf("升级后应能读出旧会话，工作区为空: %+v %v", got, err)
	}
}
