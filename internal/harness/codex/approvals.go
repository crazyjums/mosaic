package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/harness"
	"github.com/crazyjums/myllmapp/internal/permission"
)

// handleServerRequest 处理 Codex 发来的请求。审批类的转成 Mosaic 的权限确认；
// 其他需要界面参与的请求（向用户提问、MCP 征询、动态工具、令牌刷新）目前不支持，
// 明确回一个错误，让 Codex 自己继续（比起不回应让整轮卡死，这样对用户更友好）。
func handleServerRequest(ctx context.Context, req harness.Request, tr *translator, method string, params json.RawMessage) (any, error) {
	switch method {
	case "item/commandExecution/requestApproval":
		var p struct {
			ItemID  string  `json:"itemId"`
			Command *string `json:"command"`
			Cwd     *string `json:"cwd"`
			Reason  *string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		cmd := deref(p.Command)
		input, _ := json.Marshal(map[string]string{"command": cmd, "cwd": deref(p.Cwd)})
		summary := "执行命令：" + oneLine(cmd, 160)
		if r := deref(p.Reason); r != "" {
			summary += "（" + r + "）"
		}
		d, err := req.Ask(ctx, permission.Request{ID: event.NewID("perm"), CallID: p.ItemID, Tool: "shell", Risk: permission.RiskExec, Summary: summary, Input: input})
		return map[string]any{"decision": commandDecision(d, err)}, nil

	case "item/fileChange/requestApproval":
		var p struct {
			ItemID    string  `json:"itemId"`
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		_ = json.Unmarshal(params, &p)
		input, _ := json.Marshal(map[string]string{"reason": deref(p.Reason), "grant_root": deref(p.GrantRoot)})
		summary := "修改文件"
		if r := deref(p.Reason); r != "" {
			summary += "：" + oneLine(r, 160)
		} else if g := deref(p.GrantRoot); g != "" {
			summary += "（授权目录 " + g + "）"
		}
		d, err := req.Ask(ctx, permission.Request{ID: event.NewID("perm"), CallID: p.ItemID, Tool: "apply_patch", Risk: permission.RiskWrite, Summary: summary, Input: input})
		return map[string]any{"decision": fileDecision(d, err)}, nil

	// 旧协议名（老版本 Codex）：回应格式不同，这里不再兼容，直接拒绝。
	case "applyPatchApproval", "execCommandApproval":
		return map[string]any{"decision": "denied"}, nil
	}
	tr.emit("", event.Error{Code: "harness_unsupported", Message: fmt.Sprintf("Codex 请求了 Mosaic 暂不支持的交互（%s），已自动拒绝", method)})
	return nil, fmt.Errorf("mosaic 暂不支持 %s", method)
}

// commandDecision / fileDecision 把 Mosaic 的回答翻译成 Codex 的决定。出错（被取消）一律是取消整轮。
func commandDecision(d permission.Decision, err error) string {
	switch {
	case err != nil:
		return "cancel"
	case d == permission.DecisionAllowSession:
		return "acceptForSession"
	case d.Allowed():
		return "accept"
	default:
		return "decline"
	}
}

func fileDecision(d permission.Decision, err error) string { return commandDecision(d, err) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
