package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// ServerConfig 描述一个 MCP server 的连接方式（预留，供将来的配置解析使用）。
type ServerConfig struct {
	Name      string
	Transport string // "stdio" 或 "http"
	Command   string // stdio：可执行文件
	Args      []string
	Env       map[string]string
	URL       string // http：服务地址
	// Risk 是该 server 所有工具的默认风险等级。MCP 协议本身没有风险声明，保守起见默认 exec。
	Risk permission.Risk
}

// ToolInfo 是 tools/list 返回的一个工具。
type ToolInfo struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// CallResult 是 tools/call 的结果（只保留文本内容）。
type CallResult struct {
	Text    string
	IsError bool
}

// Client 是与一个 MCP server 通信的客户端。第一阶段没有实现。
type Client interface {
	ListTools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error)
	Close() error
}

// ToolAdapter 把一个 MCP 工具包装成 tool.Tool。
type ToolAdapter struct {
	Server string
	Info   ToolInfo
	Client Client
	Risk   permission.Risk
}

// QualifiedName 返回带 server 前缀的工具名，例如 mcp__github__create_issue。
func QualifiedName(server, name string) string {
	return "mcp__" + server + "__" + name
}

func (a *ToolAdapter) Spec() tool.Spec {
	risk := a.Risk
	if risk == "" {
		risk = permission.RiskExec
	}
	schema := a.Info.InputSchema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	return tool.Spec{
		Name:        QualifiedName(a.Server, a.Info.Name),
		Description: fmt.Sprintf("[MCP: %s] %s", a.Server, a.Info.Description),
		InputSchema: schema,
		Risk:        risk,
	}
}

func (a *ToolAdapter) Summarize(input json.RawMessage) string {
	s := string(input)
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return fmt.Sprintf("调用 MCP 工具 %s/%s %s", a.Server, a.Info.Name, strings.TrimSpace(s))
}

func (a *ToolAdapter) Execute(ctx context.Context, call tool.Call) tool.Result {
	r, err := a.Client.CallTool(ctx, a.Info.Name, call.Input)
	if err != nil {
		return tool.Errorf("mcp call failed: %v", err)
	}
	return tool.Result{Output: r.Text, IsError: r.IsError}
}

// RegisterServerTools 列出 server 的全部工具并注册到 reg。
func RegisterServerTools(ctx context.Context, reg *tool.Registry, server string, c Client, risk permission.Risk) error {
	tools, err := c.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("mcp: list tools of %s: %w", server, err)
	}
	for _, info := range tools {
		if err := reg.Register(&ToolAdapter{Server: server, Info: info, Client: c, Risk: risk}); err != nil {
			return err
		}
	}
	return nil
}
