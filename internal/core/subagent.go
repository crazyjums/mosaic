package core

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// 子 agent：主 agent 把一件会产生大量中间输出的活（查资料、跑测试、审代码）派出去，自己只拿到结论。
//
// 工作方式（设计见 docs/design/hooks-and-subagents.md）：
//  1. 定义来自用户目录（~/.myllmapp/agents/*.md）和插件，按来源登记在 SubagentRegistry；
//  2. 有定义时，主 agent 多一个 task 工具（参数 {agent, prompt}，描述里列出所有子 agent）；
//  3. 调用时用主 agent 这一轮的配置复制一份 AgentConfig：新 agent ID、ParentID=主 agent、子 agent 的系统提示词、
//     受限的工具表（没有 task 本身：第一版不允许嵌套），在**独立的上下文**里跑（看不到主对话历史）；
//  4. 事件照常发到总线，信封里的 agent_id/parent_id 让界面把它们挂到 task 卡片下面；开始、结束各发
//     subagent_start / subagent_end；
//  5. 子 agent 最后一条回复的文字作为 task 的结果交回主 agent；用量计入这个会话。
//
// 权限和主对话完全相同：同一个 Policy、同一个 Broker、同一个确认弹窗。子 agent 不能有比主对话更宽的权限。

// SubagentDefinition 描述一种子 agent。
type SubagentDefinition struct {
	Name        string
	Description string // 写给主 agent 看：什么时候该派这个子 agent
	System      string
	// Tools 是允许子 agent 使用的工具名；为空表示继承主 agent 的全部工具。
	Tools []string
	// DisallowedTools 先于 Tools 生效：在里面的一律去掉。
	DisallowedTools []string
	// Model 为空表示与主 agent 相同；只有当前厂商配置档里有这个模型时才换。
	Model    string
	MaxSteps int
	// Source 是定义的来源："user"（用户目录）或 "plugin:<id>"。同一来源的定义一起替换。
	Source string
	// Warnings 是加载时发现的问题（不认识的工具名等），界面上显示。
	Warnings []string
}

// SubagentRegistry 保存子 agent 定义。
type SubagentRegistry struct {
	mu   sync.RWMutex
	defs map[string]SubagentDefinition
}

func NewSubagentRegistry() *SubagentRegistry {
	return &SubagentRegistry{defs: map[string]SubagentDefinition{}}
}

// ReplaceSource 用 defs 替换来源为 source 的全部定义（用户目录重新扫描、插件启停时用）。
// 名字已被别的来源占用的定义不登记，返回它们的名字。
func (r *SubagentRegistry) ReplaceSource(source string, defs []SubagentDefinition) (skipped []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, d := range r.defs {
		if d.Source == source {
			delete(r.defs, name)
		}
	}
	for _, d := range defs {
		d.Source = source
		if d.Name == "" {
			continue
		}
		if cur, dup := r.defs[d.Name]; dup && cur.Source != source {
			skipped = append(skipped, d.Name)
			continue
		}
		r.defs[d.Name] = d
	}
	return skipped
}

func (r *SubagentRegistry) Register(d SubagentDefinition) error {
	if d.Name == "" {
		return fmt.Errorf("subagent: empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.defs[d.Name]; dup {
		return fmt.Errorf("subagent: duplicate %q", d.Name)
	}
	r.defs[d.Name] = d
	return nil
}

func (r *SubagentRegistry) Get(name string) (SubagentDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.defs[name]
	return d, ok
}

func (r *SubagentRegistry) List() []SubagentDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SubagentDefinition, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SubagentRunner 负责真正运行一个子 agent（第一阶段未实现）。
type SubagentRunner interface {
	RunSubagent(ctx context.Context, req SubagentRequest) (SubagentResult, error)
}

type SubagentRequest struct {
	ParentAgentID string
	SessionID     string
	Definition    SubagentDefinition
	Prompt        string
}

type SubagentResult struct {
	AgentID string
	Output  string
	Turn    TurnResult
}
