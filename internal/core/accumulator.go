package core

import "github.com/crazyjums/myllmapp/internal/provider"

// accumulator 把一次模型调用的 Delta 序列组装成一条 assistant 消息。
//
// 规则很简单：相邻的同类文本片段合并成一个块；类型切换时开启新块。
// 这与 Anthropic 的 content block 划分基本一致，对 OpenAI 也自然成立（通常只有一个文本块）。
//
// 为什么组装放在内核而不是厂商适配器里：见 provider.Provider 的注释——
// 一处实现，所有厂商共享；而且事件和历史消息出自同一份数据，不会出现"界面显示的和存下来的不一样"。
type accumulator struct {
	blocks []provider.Block
	usage  provider.Usage
	stop   provider.StopReason
}

func (a *accumulator) last() *provider.Block {
	if len(a.blocks) == 0 {
		return nil
	}
	return &a.blocks[len(a.blocks)-1]
}

func (a *accumulator) add(d provider.Delta) {
	switch d.Type {
	case provider.DeltaText:
		if l := a.last(); l != nil && l.Type == provider.BlockText {
			l.Text += d.Text
			return
		}
		a.blocks = append(a.blocks, provider.Block{Type: provider.BlockText, Text: d.Text})

	case provider.DeltaThinking:
		// 已经有签名的思考块是"封口"的，后续思考片段属于新的思考块。
		if l := a.last(); l != nil && l.Type == provider.BlockThinking && l.Signature == "" {
			l.Text += d.Text
			return
		}
		a.blocks = append(a.blocks, provider.Block{Type: provider.BlockThinking, Text: d.Text})

	case provider.DeltaThinkingSignature:
		// 签名属于最近的一个思考块。找不到时（理论上不会发生）新建一个空思考块保存签名，
		// 保证签名不丢——丢了签名，下一次请求会被 Anthropic 拒绝。
		for i := len(a.blocks) - 1; i >= 0; i-- {
			if a.blocks[i].Type == provider.BlockThinking {
				a.blocks[i].Signature += d.Text
				return
			}
		}
		a.blocks = append(a.blocks, provider.Block{Type: provider.BlockThinking, Signature: d.Text})

	case provider.DeltaRedactedThinking:
		a.blocks = append(a.blocks, provider.Block{Type: provider.BlockRedactedThinking, Data: d.Text})

	case provider.DeltaToolCall:
		if d.ToolCall == nil {
			return
		}
		a.blocks = append(a.blocks, provider.Block{
			Type:       provider.BlockToolUse,
			ToolCallID: d.ToolCall.ID,
			ToolName:   d.ToolCall.Name,
			ToolInput:  d.ToolCall.Input,
		})

	case provider.DeltaImage:
		if d.Image == nil {
			return
		}
		a.blocks = append(a.blocks, provider.Block{
			Type:      provider.BlockImage,
			MediaType: d.Image.MediaType,
			ImageData: d.Image.Data,
		})

	case provider.DeltaUsage:
		if d.Usage != nil {
			a.usage = *d.Usage
		}

	case provider.DeltaStop:
		a.stop = d.StopReason
	}
}

func (a *accumulator) message() provider.Message {
	return provider.Message{Role: provider.RoleAssistant, Content: a.blocks}
}

// salvagePartial 用于调用中途失败/取消的情况：只保留已经输出的文本和图片。
//   - 丢掉工具调用：工具调用必须配对 tool_result，留下"没执行也没结果"的调用会让下一次请求被厂商拒绝；
//   - 丢掉思考块：中断时思考块往往还没收到签名，Anthropic 会拒绝没有签名的思考块。
//
// 界面上这些内容已经通过事件显示过了，这里只影响"下一次发给模型的历史"。
func salvagePartial(m provider.Message) provider.Message {
	out := provider.Message{Role: m.Role}
	for _, b := range m.Content {
		if b.Type == provider.BlockText || b.Type == provider.BlockImage {
			out.Content = append(out.Content, b)
		}
	}
	return out
}
