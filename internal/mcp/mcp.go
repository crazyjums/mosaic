package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/crazyjums/myllmapp/internal/permission"
	"github.com/crazyjums/myllmapp/internal/tool"
)

// 传输方式。
const (
	TransportStdio = "stdio" // 启动本地子进程，通过标准输入输出通信
	TransportHTTP  = "http"  // Streamable HTTP：连接远程服务
)

// ServerConfig 描述一个 MCP server 的连接方式。它直接保存在 settings.json 里（设置 → MCP）。
type ServerConfig struct {
	Name      string `json:"name"`
	Transport string `json:"transport"` // stdio / http
	// stdio：可执行文件、参数、额外的环境变量和工作目录。
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	// http：服务地址和额外的请求头（常见的是 Authorization: Bearer ...）。
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Risk 是该 server 所有工具的风险等级。MCP 协议本身没有可信的风险声明，保守起见默认 exec（每次都询问）。
	Risk permission.Risk `json:"risk,omitempty"`
	// Disabled 为 true 时保留配置但不连接。
	Disabled bool `json:"disabled,omitempty"`
	// Source 是这份配置的来源：空表示用户在设置里添加的，"plugin:<id>" 表示插件带来的。
	// 由 Manager 在 Apply / ApplySource 时设置，调用方（包括前端保存设置时传来的值）填的会被覆盖。
	Source string `json:"source,omitempty"`
	// Sensitive 表示这个 server 的工具会读取私密内容（聊天记录、文档），每次调用都询问，不能被“总是允许”绕过。
	Sensitive bool `json:"sensitive,omitempty"`
}

// serverNameRe 限制 server 名：它会成为工具名的一部分，而各厂商要求工具名只含字母、数字、_ 和 -。
var serverNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Validate 检查配置是否完整。
func (c ServerConfig) Validate() error {
	if !serverNameRe.MatchString(c.Name) {
		return fmt.Errorf("MCP 名称 %q 只能包含字母、数字、_ 和 -（最多 32 个字符）", c.Name)
	}
	switch c.Transport {
	case TransportStdio:
		if strings.TrimSpace(c.Command) == "" {
			return fmt.Errorf("MCP %s：请填写要运行的命令", c.Name)
		}
	case TransportHTTP:
		if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
			return fmt.Errorf("MCP %s：地址需要以 http:// 或 https:// 开头", c.Name)
		}
	default:
		return fmt.Errorf("MCP %s：不支持的传输方式 %q", c.Name, c.Transport)
	}
	switch c.Risk {
	case "", permission.RiskRead, permission.RiskWrite, permission.RiskExec:
	default:
		return fmt.Errorf("MCP %s：未知的风险等级 %q", c.Name, c.Risk)
	}
	return nil
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

// Client 是与一个 MCP server 通信的客户端。真实实现是 RPCClient，测试里可以换成假的。
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
	// Sensitive 见 ServerConfig.Sensitive。
	Sensitive bool
}

// QualifiedName 返回带 server 前缀的工具名，例如 mcp__github__create_issue。
//
// 不合法的字符替换成 _，总长截断到 64：OpenAI 和 Anthropic 都要求工具名匹配 ^[a-zA-Z0-9_-]{1,64}$，
// 而 MCP 工具名允许点号等字符。调用 server 时用的是原始名字（ToolAdapter.Info.Name），所以替换不影响调用。
func QualifiedName(server, name string) string {
	q := "mcp__" + server + "__" + toolNameRe.ReplaceAllString(name, "_")
	if len(q) > 64 {
		q = q[:64]
	}
	return q
}

var toolNameRe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

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
		Sensitive:   a.Sensitive,
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
