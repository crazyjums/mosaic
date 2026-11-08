package mcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crazyjums/myllmapp/internal/tool"
)

// State 是一个 server 的连接状态。
type State string

const (
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateFailed     State = "failed"
	StateDisabled   State = "disabled"
)

// ServerStatus 是设置页显示的连接状态。
type ServerStatus struct {
	Name  string   `json:"name"`
	State State    `json:"state"`
	Error string   `json:"error,omitempty"`
	Tools []string `json:"tools"` // 注册进工具表的名字（带 mcp__ 前缀）
	// StartedAt 是这次连接开始的时间（连接中时界面据此显示“已等待 N 秒”）。
	StartedAt time.Time `json:"started_at,omitempty"`
	// Output 是子进程 stderr 的最后几行：连接中能看到 npx 在下载还是在报错，失败后保留作为排查依据。
	Output        string `json:"output,omitempty"`
	ServerName    string `json:"server_name,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	// Source 同 ServerConfig.Source：界面据此区分“你添加的”和“插件带来的”（后者不能在设置里直接编辑）。
	Source string `json:"source,omitempty"`
}

// conn 是 Manager 需要的连接能力：RPCClient 实现了它，测试里可以替换。
type conn interface {
	Client
	Done() <-chan struct{}
	Err() error
}

// connectTimeout 是建立连接 + 列出工具的时限；用包管理器启动的（npx、uvx 等）第一次运行要下载包，给得更宽。
// 是变量而不是常量：测试里要缩短。
var (
	connectTimeout       = 60 * time.Second
	packageRunnerTimeout = 120 * time.Second
)

// packageRunners 是会在第一次运行时联网下载依赖的启动命令（不含扩展名，不分大小写）。
var packageRunners = map[string]bool{"npx": true, "bunx": true, "uvx": true, "pipx": true, "pnpx": true, "pnpm": true, "yarn": true, "deno": true}

// timeoutFor 返回这个配置的连接时限，以及它是不是包管理器启动的（用于超时时给出更贴切的提示）。
func timeoutFor(cfg ServerConfig) (time.Duration, bool) {
	if cfg.Transport != "" && cfg.Transport != TransportStdio {
		return connectTimeout, false
	}
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(cfg.Command, "\\", "/")))
	base = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".cmd"), ".bat")
	if packageRunners[base] {
		return packageRunnerTimeout, true
	}
	return connectTimeout, false
}

// Manager 管理所有 MCP server 的连接，并把它们的工具注册进（或移出）工具注册表。
//
// 连接在后台进行：启动程序或保存设置不会因为某个 server 起得慢而卡住，界面通过 Statuses 轮询状态。
// 工具注册表本身是并发安全的，agent 每一步都会重新读取工具列表，所以连上之后下一步就能用到新工具。
type Manager struct {
	reg     *tool.Registry
	connect func(ctx context.Context, cfg ServerConfig, prog *Progress) (conn, error)

	mu      sync.Mutex
	servers map[string]*server
	order   []string
	// lists 按来源记录配置的顺序（用户的在 "" 下），order 由它合并而来。
	lists map[string][]string
}

type server struct {
	prog   *Progress
	runner bool // 是包管理器启动的（超时提示用）
	limit  time.Duration
	cfg    ServerConfig
	status ServerStatus
	client conn
	cancel context.CancelFunc
}

// NewManager 创建管理器。
func NewManager(reg *tool.Registry) *Manager {
	return &Manager{
		reg:     reg,
		servers: map[string]*server{},
		lists:   map[string][]string{},
		connect: func(ctx context.Context, cfg ServerConfig, prog *Progress) (conn, error) {
			return ConnectWith(ctx, cfg, prog)
		},
	}
}

// Apply 让用户配置的 server（Source 为空）与配置保持一致：删掉的断开，新增的连接，修改过的重连，没变的不动。
// 插件带来的 server 不受影响，见 ApplySource。
func (m *Manager) Apply(cfgs []ServerConfig) { _ = m.ApplySource("", cfgs) }

// ApplySource 和 Apply 一样，但只管理来源为 source 的 server（例如 "plugin:feishu"）。
//
// 为什么按来源分开：设置页保存的是“用户配置的完整列表”，按整份覆盖的话，插件带来的 server 会被当成
// “不在列表里”而断开；反过来，停用一个插件也只应该摘掉它自己的 server。
//
// server 名字全局唯一：名字已经被别的来源占用时，新的那个不会启动，错误里会说明（其余的照常处理）。
func (m *Manager) ApplySource(source string, cfgs []ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]ServerConfig{}
	names := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		c.Source = source
		want[c.Name] = c
		names = append(names, c.Name)
	}
	m.lists[source] = names
	m.order = m.order[:0]
	for _, src := range m.sourcesLocked() {
		m.order = append(m.order, m.lists[src]...)
	}
	for name, s := range m.servers {
		if s.cfg.Source != source {
			continue
		}
		if c, ok := want[name]; !ok || !reflect.DeepEqual(c, s.cfg) {
			m.stopLocked(s)
			delete(m.servers, name)
		}
	}
	var conflicts []string
	for _, name := range names {
		c := want[name]
		if cur, running := m.servers[name]; running {
			if cur.cfg.Source != source {
				conflicts = append(conflicts, name)
			}
			continue
		}
		m.startLocked(c)
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("mcp: server 名字已被占用: %s", strings.Join(conflicts, ", "))
	}
	return nil
}

// sourcesLocked 返回有配置列表的来源：用户的在前，其余按名字排序（让状态列表的顺序稳定）。
func (m *Manager) sourcesLocked() []string {
	out := make([]string, 0, len(m.lists))
	for src := range m.lists {
		out = append(out, src)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == "") != (out[j] == "") {
			return out[i] == ""
		}
		return out[i] < out[j]
	})
	return out
}

// Reconnect 断开并重新连接一个 server（例如修好了 server 那边的问题之后）。
func (m *Manager) Reconnect(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servers[name]
	if !ok {
		return fmt.Errorf("mcp: unknown server %q", name)
	}
	m.stopLocked(s)
	m.startLocked(s.cfg)
	return nil
}

// Statuses 按配置顺序返回所有 server 的状态。
func (m *Manager) Statuses() []ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerStatus, 0, len(m.order))
	for _, name := range m.order {
		if s, ok := m.servers[name]; ok {
			st := s.status
			if st.State == StateConnecting {
				st.Output = s.prog.Output() // 连接中：实时看子进程在输出什么
			}
			st.Tools = slices.Clone(st.Tools)
			if st.Tools == nil {
				st.Tools = []string{}
			}
			out = append(out, st)
		}
	}
	return out
}

// Close 断开所有连接（程序退出时调用）。
func (m *Manager) Close() {
	m.mu.Lock()
	servers := m.servers
	m.servers = map[string]*server{}
	m.order = nil
	for _, s := range servers {
		m.stopLocked(s)
	}
	m.mu.Unlock()
}

// ---- 内部（调用方持有 m.mu） ----

func (m *Manager) startLocked(cfg ServerConfig) {
	s := &server{cfg: cfg, prog: NewProgress(), status: ServerStatus{Name: cfg.Name, State: StateConnecting, Source: cfg.Source, StartedAt: time.Now()}}
	m.servers[cfg.Name] = s
	if cfg.Disabled {
		s.status.State, s.status.StartedAt = StateDisabled, time.Time{}
		return
	}
	s.limit, s.runner = timeoutFor(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), s.limit)
	s.cancel = cancel
	go m.run(ctx, s)
}

// stopLocked 移除 server 的工具并断开连接。关闭子进程可能要等几秒，放到后台做。
func (m *Manager) stopLocked(s *server) {
	if s.cancel != nil {
		s.cancel()
	}
	m.unregisterLocked(s)
	if c := s.client; c != nil {
		s.client = nil
		go c.Close()
	}
}

func (m *Manager) unregisterLocked(s *server) {
	for _, n := range s.status.Tools {
		m.reg.Unregister(n)
	}
	s.status.Tools = nil
}

// current 判断 s 是否仍是这个名字的当前 server（Apply / Reconnect 可能已经把它换掉了）。
func (m *Manager) current(s *server) bool { return m.servers[s.cfg.Name] == s }

// run 在后台连接一个 server、注册工具，然后等待连接断开。
func (m *Manager) run(ctx context.Context, s *server) {
	defer s.cancel()
	c, err := m.connect(ctx, s.cfg, s.prog)
	var tools []ToolInfo
	if err == nil {
		tools, err = c.ListTools(ctx)
		if err != nil {
			c.Close()
		}
	}
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// 超时：把“context deadline exceeded”换成人能看懂的话，包管理器启动的还要提示最常见的原因。
		msg := fmt.Sprintf("连接超时（%s 内没有完成握手）", s.limit.Round(time.Second))
		if s.runner {
			msg += "。首次运行需要下载依赖，请检查网络、npm/PyPI 镜像或代理（公司网络要能访问包仓库，自签证书需要设置 NODE_EXTRA_CA_CERTS）"
		}
		err = errors.New(msg)
	}

	m.mu.Lock()
	if !m.current(s) {
		m.mu.Unlock()
		if err == nil {
			c.Close()
		}
		return
	}
	if err != nil {
		s.status.State, s.status.Error = StateFailed, err.Error()
		s.status.Output = s.prog.Output()
		m.mu.Unlock()
		return
	}
	s.client = c
	s.status.State = StateConnected
	if rc, ok := c.(*RPCClient); ok {
		s.status.ServerName, s.status.ServerVersion = rc.ServerName, rc.ServerVersion
	}
	var skipped []string
	for _, info := range tools {
		a := &ToolAdapter{Server: s.cfg.Name, Info: info, Client: c, Risk: s.cfg.Risk, Sensitive: s.cfg.Sensitive}
		// 重名（例如两个工具名只差一个点号，替换后相同）时跳过后一个，而不是让整个 server 不可用。
		if err := m.reg.Register(a); err != nil {
			skipped = append(skipped, info.Name)
			continue
		}
		s.status.Tools = append(s.status.Tools, a.Spec().Name)
	}
	if len(skipped) > 0 {
		s.status.Error = "以下工具因重名被跳过：" + strings.Join(skipped, ", ")
	}
	m.mu.Unlock()

	// 等待连接断开（stdio 子进程退出）。HTTP 传输是无连接的，Done 为 nil，不需要等。
	done := c.Done()
	if done == nil {
		return
	}
	<-done
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.current(s) || s.client != c {
		return
	}
	m.unregisterLocked(s)
	s.client = nil
	s.status.State = StateFailed
	s.status.Error = "连接已断开"
	if e := c.Err(); e != nil {
		s.status.Error += "：" + e.Error()
	}
}
