package core

import (
	"context"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// Session 是一个会话：一串中立格式的消息，加上元数据和累计用量。
//
// 字段公开是为了让 store 能直接序列化；但运行期间 agent 会并发修改它（同时界面可能读状态），
// 所以修改都走带锁的方法，读取用 Snapshot 拿一份副本。
type Session struct {
	ID        string
	Title     string
	Provider  string // 配置档名称
	Model     string
	Messages  []provider.Message
	Usage     provider.Usage
	CreatedAt time.Time
	UpdatedAt time.Time

	mu sync.Mutex
}

// NewSession 创建一个空会话。
func NewSession(id, profile, model string) *Session {
	now := time.Now()
	return &Session{ID: id, Provider: profile, Model: model, CreatedAt: now, UpdatedAt: now}
}

// Snapshot 返回会话的深拷贝（消息切片复制，块内容是值类型/不可变的 RawMessage，浅拷贝即可）。
func (s *Session) Snapshot() *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Session{
		ID:        s.ID,
		Title:     s.Title,
		Provider:  s.Provider,
		Model:     s.Model,
		Messages:  append([]provider.Message(nil), s.Messages...),
		Usage:     s.Usage,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

func (s *Session) appendMessage(m provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, m)
	s.UpdatedAt = time.Now()
	// 用第一条用户消息做标题：简单、零成本。以后可以换成让模型生成标题。
	if s.Title == "" && m.Role == provider.RoleUser {
		if t := m.Text(); t != "" {
			s.Title = truncateRunes(t, 40)
		}
	}
}

func (s *Session) replaceMessages(msgs []provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = msgs
	s.UpdatedAt = time.Now()
}

func (s *Session) messages() []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.Message(nil), s.Messages...)
}

func (s *Session) addUsage(u provider.Usage) provider.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Usage = s.Usage.Add(u)
	return s.Usage
}

func (s *Session) setModel(profile, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Provider, s.Model = profile, model
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// SessionSummary 是会话列表里的一项，不含消息正文。
type SessionSummary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ErrSessionNotFound 由 SessionStore 在会话不存在时返回。
var ErrSessionNotFound = errors.New("session not found")

// SessionStore 是内核需要的持久化能力。
//
// 接口定义在内核（使用方）而不是 store（实现方），这是 Go 惯用的"接口由使用者定义"：
// 内核因此不依赖 SQLite，测试可以传内存实现，将来换成文件/远端存储也不用改内核。
type SessionStore interface {
	SaveSession(ctx context.Context, s *Session) error
	LoadSession(ctx context.Context, id string) (*Session, error)
	ListSessions(ctx context.Context) ([]SessionSummary, error)
	DeleteSession(ctx context.Context, id string) error
}
