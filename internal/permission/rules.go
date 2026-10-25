package permission

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Rule 是一条细粒度的权限规则，例如"shell 执行 go test* 总是允许""shell 执行 rm * 总是拒绝"。
//
// 规则比档位（Mode）更具体，所以优先级更高：命中规则就按规则处理，没有命中才看档位。
// 这意味着 deny 规则在"全部放行"档位下也生效（适合兜底禁止危险命令），
// allow 规则在"只读"档位下也生效（适合"只读，但允许跑测试"）。
type Rule struct {
	// Tool 是工具名，"*" 表示所有工具。
	Tool string `json:"tool"`
	// Pattern 匹配这次调用的"主体"：shell 是命令，文件类工具是路径，其他工具是整段参数 JSON。
	// 支持 * 和 ? 通配符（* 可以跨越 /）；为空表示匹配所有调用。
	Pattern string `json:"pattern"`
	// Action 是命中后的裁决：allow / ask / deny。
	Action Verdict `json:"action"`
}

// Validate 检查规则是否合法。
func (r Rule) Validate() error {
	if strings.TrimSpace(r.Tool) == "" {
		return fmt.Errorf("规则缺少工具名（所有工具用 *）")
	}
	switch r.Action {
	case Allow, Ask, Deny:
	default:
		return fmt.Errorf("规则的动作 %q 无效（应为 allow / ask / deny）", r.Action)
	}
	return nil
}

// Matches 判断规则是否命中一次调用。
func (r Rule) Matches(tool string, input json.RawMessage) bool {
	if r.Tool != "*" && r.Tool != tool {
		return false
	}
	return r.Pattern == "" || Wildcard(r.Pattern, Subject(input))
}

// Subject 从工具参数中取出用于匹配的"主体"：优先 command，其次 path、url，都没有时用整段 JSON。
// 用字段名而不是工具名来判断，这样 MCP 工具只要参数里有 path / command 也能写规则。
func Subject(input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) == nil {
		for _, k := range []string{"command", "path", "url"} {
			if s, ok := m[k].(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return string(input)
}

// Wildcard 是不区分路径分隔符的通配符匹配：* 匹配任意长度（包括 /），? 匹配一个字符。
//
// 没有用 path.Match / filepath.Match：它们的 * 不能跨越 /，"src/*" 匹配不到 "src/a/b.go"，
// 对命令行（"go test ./..."）也不合适。规则的写法应该尽量直白。
func Wildcard(pattern, s string) bool {
	p, t := []rune(pattern), []rune(s)
	// 经典的贪心回溯算法：记住最近一个 * 的位置，失配时让它多吃一个字符。
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// RuleSetter 是可选能力：支持细粒度规则的策略实现它。
type RuleSetter interface {
	Rules() []Rule
	SetRules([]Rule) error
}

// RulePolicy 在另一个策略（通常是 ModePolicy）之前加一层规则：命中规则按规则裁决，否则交给内层策略。
//
// 用"包装"而不是把规则塞进 ModePolicy：档位和规则是两个独立的概念，分开后各自更容易读懂和测试，
// 以后也可以给别的策略套上规则。
type RulePolicy struct {
	inner Policy

	mu    sync.RWMutex
	rules []Rule
}

// NewRulePolicy 用规则包装一个策略。
func NewRulePolicy(inner Policy, rules []Rule) *RulePolicy {
	p := &RulePolicy{inner: inner}
	_ = p.SetRules(rules)
	return p
}

// Evaluate 按顺序匹配规则，第一条命中的规则生效；都没有命中时交给内层策略。
func (p *RulePolicy) Evaluate(tool string, risk Risk, input json.RawMessage) Verdict {
	p.mu.RLock()
	rules := p.rules
	p.mu.RUnlock()
	for _, r := range rules {
		if r.Matches(tool, input) {
			return r.Action
		}
	}
	return p.inner.Evaluate(tool, risk, input)
}

func (p *RulePolicy) Remember(tool string, d Decision) { p.inner.Remember(tool, d) }
func (p *RulePolicy) Reset()                           { p.inner.Reset() }

func (p *RulePolicy) Rules() []Rule {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Rule(nil), p.rules...)
}

// SetRules 替换全部规则。有一条不合法就整体拒绝，不会只生效一半。
func (p *RulePolicy) SetRules(rules []Rule) error {
	for i, r := range rules {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("第 %d 条规则：%w", i+1, err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = append([]Rule(nil), rules...)
	return nil
}

// 档位相关的操作转发给内层策略，这样包装之后状态栏切换档位仍然有效。

func (p *RulePolicy) Mode() Mode {
	if ms, ok := p.inner.(ModeSwitcher); ok {
		return ms.Mode()
	}
	return ""
}

func (p *RulePolicy) SetMode(m Mode) error {
	if ms, ok := p.inner.(ModeSwitcher); ok {
		return ms.SetMode(m)
	}
	return fmt.Errorf("permission: inner policy does not support modes")
}
