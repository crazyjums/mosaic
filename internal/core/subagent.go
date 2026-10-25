package core

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// 本文件是"子 agent"能力的扩展点：定义与注册已实现，调度（SubagentRunner）只有接口。
//
// 设想中的工作方式（详见 docs/roadmap.md 和 docs/extending.md）：
//  1. 用户/插件通过 SubagentRegistry.Register 注册子 agent 定义（名称、用途、系统提示词、可用工具）；
//  2. 内核把所有定义包装成一个名为 "task" 的工具暴露给主 agent，参数是 {agent, prompt}；
//  3. 主 agent 调用该工具时，SubagentRunner 用一份新的 AgentConfig（新 ID、ParentID=调用者、
//     受限的工具注册表）创建一个 Agent，在独立的 Session 里运行；
//  4. 子 agent 的所有事件照常发到同一个总线，信封里的 agent_id/parent_id 让前端把它们挂到树上；
//     开始和结束时额外发 subagent_start / subagent_end；
//  5. 子 agent 的最终文本作为 tool_result 返回给主 agent。
//
// 因为 Agent 本身是无状态的、所有依赖都通过 AgentConfig 注入，第 3 步不需要修改 Agent。

// SubagentDefinition 描述一种子 agent。
type SubagentDefinition struct {
	Name        string
	Description string // 写给主 agent 看：什么时候该派这个子 agent
	System      string
	// Tools 是允许子 agent 使用的工具名；为空表示继承主 agent 的全部工具。
	Tools []string
	// Model 为空表示与主 agent 相同。
	Model    string
	MaxSteps int
}

// SubagentRegistry 保存子 agent 定义。
type SubagentRegistry struct {
	mu   sync.RWMutex
	defs map[string]SubagentDefinition
}

func NewSubagentRegistry() *SubagentRegistry {
	return &SubagentRegistry{defs: map[string]SubagentDefinition{}}
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
