package core

import (
	"encoding/json"
	"sync"

	"github.com/crazyjums/myllmapp/internal/permission"
)

// 按会话记录“本会话总是允许”。
//
// 为什么不用 permission.ModePolicy 自带的记忆：它是整个应用共用的一份，原来靠“切换会话时 Reset”来假装按会话隔离。
// 多个会话同时运行后，切换会话不应该清掉后台会话的授权，更不能让 A 会话里点的“总是允许”在 B 会话生效。
// 所以每个运行中的会话拿到一个按会话 ID 记账的包装策略（sessionPolicy）：档位和规则仍然是全局的，只有“总是允许”按会话隔离。
type grantBook struct {
	mu sync.Mutex
	m  map[string]map[string]bool // 会话 ID → 工具名 → 已允许
}

func newGrantBook() *grantBook { return &grantBook{m: map[string]map[string]bool{}} }

func (g *grantBook) allowed(sid, tool string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.m[sid][tool]
}

func (g *grantBook) add(sid, tool string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m[sid] == nil {
		g.m[sid] = map[string]bool{}
	}
	g.m[sid][tool] = true
}

// reset 清掉一个会话的授权。
func (g *grantBook) reset(sid string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.m, sid)
}

// resetAll 清掉所有会话的授权（默认工作区变了，跟随默认工作区的会话都换了目录）。
func (g *grantBook) resetAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.m = map[string]map[string]bool{}
}

// sessionPolicy 是交给某个会话的 Agent 的策略：先问全局策略（档位 + 规则），
// 需要确认时再看这个会话是否已经“总是允许”过。Remember 只记在本会话，不写进全局策略。
type sessionPolicy struct {
	permission.Policy
	grants *grantBook
	sid    string
}

func (p *sessionPolicy) Evaluate(tool string, risk permission.Risk, input json.RawMessage) permission.Verdict {
	v := p.Policy.Evaluate(tool, risk, input)
	if v == permission.Ask && p.grants.allowed(p.sid, tool) {
		return permission.Allow
	}
	return v
}

func (p *sessionPolicy) Remember(tool string, d permission.Decision) {
	if d == permission.DecisionAllowSession {
		p.grants.add(p.sid, tool)
	}
}

func (p *sessionPolicy) Reset() { p.grants.reset(p.sid) }
