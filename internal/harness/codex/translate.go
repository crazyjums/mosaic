package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/provider"
)

// translator 把 app-server 推来的通知翻译成 Mosaic 的统一事件，同时累积出要存进会话历史的消息。
//
// 对应关系（Codex 的“项目 item” → Mosaic）：
//
//	agentMessage       → 一条助手消息：message_start、text_delta…、message_end
//	reasoning          → thinking_delta（挂在当前助手消息上）
//	commandExecution   → tool_call(shell) … tool_result（命令输出）
//	fileChange         → tool_call(apply_patch) … tool_result
//	mcpToolCall        → tool_call(mcp:<server>/<tool>) … tool_result
//	webSearch          → tool_call(web_search) … tool_result
//
// 每条助手消息的块顺序是：思考、文本、它之后发起的工具调用；工具结果放进紧随其后的 user 消息，
// 和内置循环存的结构一致（所以会话重放、导出、换内置引擎继续聊都能用）。
type translator struct {
	emit     func(messageID string, p event.Payload)
	provider string
	model    string

	step     int
	msgID    string // 当前打开的助手消息；空表示没有
	cur      []provider.Block
	results  []provider.Block
	messages []provider.Message

	text     *strings.Builder // 当前 agentMessage 的文本
	thinking *strings.Builder

	calls    map[string]*toolState // itemId → 状态
	order    []string              // 本条助手消息里发起的 callId，保证每个 tool_use 都有 tool_result
	usage    provider.Usage
	lastErr  string
	finished bool
	turnID   string
	status   string
}

type toolState struct {
	name   string
	output strings.Builder
	done   bool
}

func newTranslator(model, providerName string, emit func(string, event.Payload)) *translator {
	return &translator{emit: emit, provider: providerName, model: model, calls: map[string]*toolState{}}
}

// openMessage 保证有一条打开的助手消息（工具调用、思考可能在文本之前到达）。
func (t *translator) openMessage() {
	if t.msgID != "" {
		return
	}
	t.step++
	t.msgID = event.NewID("msg")
	t.emit(t.msgID, event.MessageStart{Provider: t.provider, Model: t.model, Step: t.step})
}

// msgHasContent 报告当前打开的助手消息里有没有文本或工具调用（只有思考不算）。
func (t *translator) msgHasContent() bool {
	if t.msgID == "" {
		return false
	}
	if t.text != nil && t.text.Len() > 0 {
		return true
	}
	for _, b := range t.cur {
		if b.Type == provider.BlockText || b.Type == provider.BlockToolUse {
			return true
		}
	}
	return false
}

// closeMessage 结束当前助手消息，并把已累积的块和工具结果落进 messages。
func (t *translator) closeMessage(stop string) {
	t.flushText()
	t.flushThinking()
	if t.msgID == "" {
		return
	}
	t.emit(t.msgID, event.MessageEnd{StopReason: stop})
	t.msgID = ""
}

// flushPair 把 cur 和 results 作为一对消息落地（助手消息 + 工具结果的 user 消息）。
func (t *translator) flushPair() {
	if len(t.cur) == 0 && len(t.results) == 0 {
		return
	}
	// 每个 tool_use 必须有 tool_result，否则这段历史没法发给任何模型。
	have := map[string]bool{}
	for _, b := range t.results {
		have[b.ToolCallID] = true
	}
	for _, id := range t.order {
		if !have[id] {
			t.results = append(t.results, provider.Block{Type: provider.BlockToolResult, ToolCallID: id, Text: "（没有拿到结果：这一轮在它完成前结束了）", IsError: true})
		}
	}
	t.order = nil
	if len(t.cur) > 0 {
		t.messages = append(t.messages, provider.Message{Role: provider.RoleAssistant, Content: t.cur})
	}
	if len(t.results) > 0 {
		t.messages = append(t.messages, provider.Message{Role: provider.RoleUser, Content: t.results})
	}
	t.cur, t.results = nil, nil
}

func (t *translator) flushText() {
	if t.text != nil && t.text.Len() > 0 {
		t.cur = append(t.cur, provider.Block{Type: provider.BlockText, Text: t.text.String()})
	}
	t.text = nil
}

func (t *translator) flushThinking() {
	if t.thinking != nil && t.thinking.Len() > 0 {
		t.cur = append(t.cur, provider.Block{Type: provider.BlockThinking, Text: t.thinking.String()})
	}
	t.thinking = nil
}

// finish 在一轮结束时收尾：关掉还开着的消息，把没落地的块落地。
func (t *translator) finish(stop string) []provider.Message {
	t.closeMessage(stop)
	t.flushPair()
	return t.messages
}

// ---- 通知 ----

type itemEnvelope struct {
	Item   json.RawMessage `json:"item"`
	TurnID string          `json:"turnId"`
}

type threadItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	// agentMessage
	Text string `json:"text"`

	// commandExecution
	Command          string  `json:"command"`
	Cwd              string  `json:"cwd"`
	AggregatedOutput *string `json:"aggregatedOutput"`
	ExitCode         *int    `json:"exitCode"`
	DurationMs       *int64  `json:"durationMs"`
	Status           string  `json:"status"`

	// fileChange
	Changes []struct {
		Path string          `json:"path"`
		Kind json.RawMessage `json:"kind"`
		Diff string          `json:"diff"`
	} `json:"changes"`

	// mcpToolCall
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`

	// webSearch
	Query string `json:"query"`
}

// handle 处理一条通知。返回 true 表示这一轮结束了（turn/completed）。
func (t *translator) handle(method string, params json.RawMessage) (done bool) {
	switch method {
	case "turn/started":
		var p struct {
			Turn struct{ ID string } `json:"turn"`
		}
		_ = json.Unmarshal(params, &p)
		t.turnID = p.Turn.ID
	case "item/started":
		var p itemEnvelope
		if json.Unmarshal(params, &p) != nil {
			return false
		}
		var it threadItem
		if json.Unmarshal(p.Item, &it) != nil {
			return false
		}
		t.itemStarted(it)
	case "item/agentMessage/delta":
		var p struct{ Delta string }
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			t.openMessage()
			t.flushThinking() // 块顺序和到达顺序一致：思考在先，文本在后
			if t.text == nil {
				t.text = &strings.Builder{}
			}
			t.text.WriteString(p.Delta)
			t.emit(t.msgID, event.TextDelta{Text: p.Delta})
		}
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta":
		var p struct{ Delta string }
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			t.openMessage()
			t.flushText()
			if t.thinking == nil {
				t.thinking = &strings.Builder{}
			}
			t.thinking.WriteString(p.Delta)
			t.emit(t.msgID, event.ThinkingDelta{Text: p.Delta})
		}
	case "item/commandExecution/outputDelta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string
		}
		if json.Unmarshal(params, &p) == nil {
			if st := t.calls[p.ItemID]; st != nil {
				st.output.WriteString(p.Delta)
			}
		}
	case "item/completed":
		var p itemEnvelope
		if json.Unmarshal(params, &p) != nil {
			return false
		}
		var it threadItem
		if json.Unmarshal(p.Item, &it) != nil {
			return false
		}
		t.itemCompleted(it)
	case "thread/tokenUsage/updated":
		var p struct {
			TokenUsage struct {
				Last struct {
					InputTokens       int `json:"inputTokens"`
					CachedInputTokens int `json:"cachedInputTokens"`
					OutputTokens      int `json:"outputTokens"`
				} `json:"last"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(params, &p) == nil {
			l := p.TokenUsage.Last
			// 一轮里会更新多次（每次模型调用一次），累加成本轮合计。
			t.usage = t.usage.Add(provider.Usage{InputTokens: l.InputTokens - l.CachedInputTokens, CacheReadTokens: l.CachedInputTokens, OutputTokens: l.OutputTokens})
		}
	case "error":
		var p struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			WillRetry bool `json:"willRetry"`
		}
		if json.Unmarshal(params, &p) == nil && p.Error.Message != "" {
			if p.WillRetry {
				t.emit("", event.Error{Code: "harness_retry", Message: p.Error.Message})
			} else {
				t.lastErr = p.Error.Message
			}
		}
	case "warning":
		var p struct{ Message string }
		if json.Unmarshal(params, &p) == nil && p.Message != "" {
			t.emit("", event.Error{Code: "harness_warning", Message: p.Message})
		}
	case "turn/completed":
		var p struct {
			Turn struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(params, &p) == nil {
			if t.turnID != "" && p.Turn.ID != "" && p.Turn.ID != t.turnID {
				return false // 别的 turn（不会发生，防御）
			}
			t.status = p.Turn.Status
			if p.Turn.Error != nil && p.Turn.Error.Message != "" {
				t.lastErr = p.Turn.Error.Message
			}
		}
		t.finished = true
		return true
	}
	return false
}

func (t *translator) itemStarted(it threadItem) {
	switch it.Type {
	case "agentMessage":
		// 只有上一条消息已经说过话或发起过工具调用时才另起一条助手消息（并让它的工具结果先落地）；
		// 只有思考的话，思考和紧跟着的回答属于同一条。
		if t.msgHasContent() {
			t.closeMessage("tool_use")
			t.flushPair()
		}
		t.openMessage()
		t.text = &strings.Builder{}
	case "reasoning":
		t.openMessage()
	case "commandExecution":
		input, _ := json.Marshal(map[string]string{"command": it.Command, "cwd": it.Cwd})
		t.startTool(it.ID, "shell", "exec", input)
	case "fileChange":
		type ch struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		}
		var list []ch
		for _, c := range it.Changes {
			list = append(list, ch{Path: c.Path, Kind: kindName(c.Kind)})
		}
		input, _ := json.Marshal(map[string]any{"changes": list})
		t.startTool(it.ID, "apply_patch", "write", input)
	case "mcpToolCall":
		args := it.Arguments
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		t.startTool(it.ID, "mcp:"+it.Server+"/"+it.Tool, "exec", args)
	case "webSearch":
		input, _ := json.Marshal(map[string]string{"query": it.Query})
		t.startTool(it.ID, "web_search", "read", input)
	}
}

func (t *translator) startTool(id, name, risk string, input json.RawMessage) {
	if _, dup := t.calls[id]; dup {
		return
	}
	t.openMessage()
	t.flushText()
	t.flushThinking()
	t.calls[id] = &toolState{name: name}
	t.order = append(t.order, id)
	t.cur = append(t.cur, provider.Block{Type: provider.BlockToolUse, ToolCallID: id, ToolName: name, ToolInput: input})
	t.emit(t.msgID, event.ToolCall{CallID: id, Name: name, Input: input, Risk: risk})
}

func (t *translator) itemCompleted(it threadItem) {
	switch it.Type {
	case "agentMessage":
		// 增量没发过（极短的消息可能只有 completed）时用完整文本补上。
		if t.text == nil || t.text.Len() == 0 {
			if it.Text != "" {
				t.openMessage()
				t.text = &strings.Builder{}
				t.text.WriteString(it.Text)
				t.emit(t.msgID, event.TextDelta{Text: it.Text})
			}
		}
		t.flushThinking()
		t.flushText()
	case "commandExecution":
		st := t.calls[it.ID]
		if st == nil {
			return
		}
		out := st.output.String()
		if it.AggregatedOutput != nil && *it.AggregatedOutput != "" {
			out = *it.AggregatedOutput
		}
		isErr := it.Status == "failed" || it.Status == "declined" || (it.ExitCode != nil && *it.ExitCode != 0)
		if it.Status == "declined" && out == "" {
			out = "用户拒绝了这条命令"
		}
		if it.ExitCode != nil && *it.ExitCode != 0 {
			out += fmt.Sprintf("\n[退出码 %d]", *it.ExitCode)
		}
		t.finishTool(it.ID, out, isErr, it.DurationMs)
	case "fileChange":
		var b strings.Builder
		for _, c := range it.Changes {
			fmt.Fprintf(&b, "%s %s\n", kindName(c.Kind), c.Path)
		}
		out := strings.TrimSpace(b.String())
		isErr := it.Status == "failed" || it.Status == "declined"
		if it.Status == "declined" {
			out = "用户拒绝了这次修改"
		}
		t.finishTool(it.ID, out, isErr, nil)
	case "mcpToolCall":
		out := string(it.Result)
		isErr := it.Status == "failed"
		if isErr && len(it.Error) > 0 && string(it.Error) != "null" {
			out = string(it.Error)
		}
		t.finishTool(it.ID, out, isErr, it.DurationMs)
	case "webSearch":
		t.finishTool(it.ID, "（搜索已完成）", false, nil)
	}
}

func (t *translator) finishTool(id, out string, isErr bool, durMs *int64) {
	st := t.calls[id]
	if st == nil || st.done {
		return
	}
	st.done = true
	var d int64
	if durMs != nil {
		d = *durMs
	}
	t.emit("", event.ToolResult{CallID: id, Name: st.name, Output: out, IsError: isErr, DurationMS: d})
	t.results = append(t.results, provider.Block{Type: provider.BlockToolResult, ToolCallID: id, Text: out, IsError: isErr})
}

// kindName 把 PatchChangeKind（{"type":"add"} 之类）压成一个词。
func kindName(raw json.RawMessage) string {
	var k struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &k) == nil && k.Type != "" {
		return k.Type
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return "update"
}
