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
	ID       string
	Title    string
	Provider string // 配置档名称
	Model    string
	// Workspace 是这个会话的工作区（绝对路径）。为空表示跟随默认工作区（设置 → 常规），
	// 旧版本保存的会话都是这样。一个会话通常对应一个代码仓库，所以工作区跟着会话走。
	Workspace string
	Messages  []provider.Message
	Usage     provider.Usage
	CreatedAt time.Time
	UpdatedAt time.Time
	// epoch 在整段消息被替换（压缩上下文）时加一。界面按"消息下标"分页加载历史，
	// 消息被替换后下标不再对应原来的位置，据此让分页请求失败，而不是返回错位的内容。
	epoch int

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
		Workspace: s.Workspace,
		Messages:  append([]provider.Message(nil), s.Messages...),
		Usage:     s.Usage,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		epoch:     s.epoch,
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
	s.epoch++
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

func (s *Session) setTitle(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Title = t
}

func (s *Session) setWorkspace(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Workspace = dir
}

func (s *Session) workspace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Workspace
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
	Workspace    string    `json:"workspace"` // 为空表示跟随默认工作区
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
