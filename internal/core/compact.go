package core

import (
	"context"

	"github.com/crazyjums/myllmapp/internal/provider"
)

// Compactor 是上下文压缩的扩展点（第一阶段只有 no-op 实现）。
//
// 调用时机：agent 循环每一步调用模型之前（见 Agent.prepareContext）。
// 返回 Changed=true 时，内核会用新的消息列表替换会话历史，并发出 context_compacted 事件。
//
// 为什么放在"每一步之前"而不是"每一轮之前"：一轮里工具结果可能很大（读了好几个文件），
// 上下文在轮中就可能超限，必须在每次请求前都有机会压缩。
//
// 后续实现思路（见 docs/roadmap.md）：
//   - 估算 token（或用厂商返回的 input_tokens）超过窗口的某个比例时触发；
//   - 用同一个 Provider 对较早的消息生成摘要，替换为一条 user 消息 + 保留最近 N 轮；
//   - 注意不能把 tool_use 和它的 tool_result 拆开。
type Compactor interface {
	Compact(ctx context.Context, req CompactRequest) (CompactResult, error)
}

type CompactRequest struct {
	Messages []provider.Message
	Model    string
}

type CompactResult struct {
	Changed      bool
	Messages     []provider.Message
	BeforeTokens int
	AfterTokens  int
	Summary      string
}

// NoopCompactor 从不压缩。
type NoopCompactor struct{}

func (NoopCompactor) Compact(_ context.Context, req CompactRequest) (CompactResult, error) {
	return CompactResult{Messages: req.Messages}, nil
}
