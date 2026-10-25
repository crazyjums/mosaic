package store

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/crazyjums/myllmapp/internal/core"
)

// Memory 是内存中的会话存储，进程退出即丢失。用于测试和 CLI 的 --no-store 模式。
type Memory struct {
	mu       sync.Mutex
	sessions map[string]*core.Session
	use      *memUsage // 用量账本，见 usage.go
}

var _ core.SessionStore = (*Memory)(nil)

func NewMemory() *Memory { return &Memory{sessions: map[string]*core.Session{}} }

// 存取都做快照拷贝，模拟真实存储"存进去的是副本"的语义，避免测试因为共享指针而误判。

func (m *Memory) SaveSession(_ context.Context, s *core.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s.Snapshot()
	return nil
}

func (m *Memory) LoadSession(_ context.Context, id string) (*core.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", core.ErrSessionNotFound, id)
	}
	return s.Snapshot(), nil
}

func (m *Memory) ListSessions(_ context.Context) ([]core.SessionSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]core.SessionSummary, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, core.SessionSummary{
			ID: s.ID, Title: s.Title, Provider: s.Provider, Model: s.Model, Workspace: s.Workspace,
			MessageCount: len(s.Messages), CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (m *Memory) DeleteSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return nil
}
