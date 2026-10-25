package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// todo_write / todo_read：给模型一个"任务清单"，多步骤任务时先列计划、做完一项勾一项。
//
// 清单按会话保存在内存里：它只是模型的工作笔记，不值得落盘；重启应用后模型可以重新列。
// 只改内存、不碰工作区，所以是 read 风险，免确认。

type todoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | in_progress | completed
}

type todoStore struct {
	mu    sync.Mutex
	lists map[string][]todoItem // 会话 ID → 清单
}

func newTodoStore() *todoStore { return &todoStore{lists: map[string][]todoItem{}} }

var todoMarks = map[string]string{"pending": "[ ]", "in_progress": "[~]", "completed": "[x]"}

func formatTodos(items []todoItem) string {
	if len(items) == 0 {
		return "(任务清单为空)"
	}
	var b strings.Builder
	done := 0
	for i, it := range items {
		if it.Status == "completed" {
			done++
		}
		fmt.Fprintf(&b, "%s %d. %s\n", todoMarks[it.Status], i+1, it.Content)
	}
	fmt.Fprintf(&b, "进度：%d/%d", done, len(items))
	return b.String()
}

// TodoWrite 用新清单整体替换旧清单。
type TodoWrite struct{ store *todoStore }

type todoWriteInput struct {
	Items []todoItem `json:"items"`
}

func (t *TodoWrite) Spec() tool.Spec {
	return tool.Spec{
		Name:        "todo_write",
		Description: "更新当前会话的任务清单（整体替换）。多步骤任务开始前列出计划，每完成一项就更新状态。同一时间最多一项 in_progress。",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "items": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "content": {"type": "string", "description": "任务描述"},
          "status": {"type": "string", "enum": ["pending", "in_progress", "completed"]}
        },
        "required": ["content", "status"]
      }
    }
  },
  "required": ["items"]
}`),
		Risk: permission.RiskRead,
	}
}

func (t *TodoWrite) Summarize(input json.RawMessage) string {
	var in todoWriteInput
	_ = json.Unmarshal(input, &in)
	return fmt.Sprintf("更新任务清单（%d 项）", len(in.Items))
}

func (t *TodoWrite) Execute(_ context.Context, call tool.Call) tool.Result {
	var in todoWriteInput
	if err := decode(call.Input, &in); err != nil {
		return tool.Errorf("%v", err)
	}
	for i, it := range in.Items {
		if _, ok := todoMarks[it.Status]; !ok {
			return tool.Errorf("第 %d 项的 status %q 无效，只能是 pending / in_progress / completed", i+1, it.Status)
		}
		if strings.TrimSpace(it.Content) == "" {
			return tool.Errorf("第 %d 项的 content 为空", i+1)
		}
	}
	t.store.mu.Lock()
	t.store.lists[call.SessionID] = in.Items
	t.store.mu.Unlock()
	return tool.Result{Output: formatTodos(in.Items)}
}

// TodoRead 读取当前会话的任务清单。
type TodoRead struct{ store *todoStore }

func (t *TodoRead) Spec() tool.Spec {
	return tool.Spec{
		Name:        "todo_read",
		Description: "读取当前会话的任务清单。",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Risk:        permission.RiskRead,
	}
}

func (t *TodoRead) Summarize(json.RawMessage) string { return "读取任务清单" }

func (t *TodoRead) Execute(_ context.Context, call tool.Call) tool.Result {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	return tool.Result{Output: formatTodos(t.store.lists[call.SessionID])}
}
