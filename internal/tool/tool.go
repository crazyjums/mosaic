package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/crazyjums/myllmapp/internal/permission"
)

// Spec 是工具的静态描述。
type Spec struct {
	Name        string
	Description string
	// InputSchema 是参数的 JSON Schema（object 类型），原样发给模型。
	// 用 json.RawMessage 手写 schema 而不是从 Go 结构体反射生成：
	// schema 是写给模型看的"文档"，手写能精确控制描述文字，也少一个依赖。
	InputSchema json.RawMessage
	Risk        permission.Risk
}

// Call 是一次工具调用的上下文。
type Call struct {
	ID    string
	Input json.RawMessage
	// Workspace 是工作区根目录的绝对路径。文件类工具必须把路径限制在它之内。
	Workspace string
}

// Result 是工具执行结果，会作为 tool_result 回传给模型，同时展示在界面的工具卡片上。
//
// 注意：工具"执行失败"（文件不存在、命令退出码非 0）用 IsError=true 表达，而不是返回 Go error。
// 因为这类失败是模型需要看到并据此调整的信息（例如换个路径再试），
// 而不是应该中断 agent 循环的程序错误。
type Result struct {
	Output  string
	IsError bool
}

// Errorf 是构造失败结果的便捷函数。
func Errorf(format string, args ...any) Result {
	return Result{Output: fmt.Sprintf(format, args...), IsError: true}
}

// Tool 是所有工具都要实现的接口。
//
// 新增工具的步骤：实现这三个方法 → Registry.Register。不需要修改内核。
type Tool interface {
	Spec() Spec
	// Summarize 用一句人话描述这次调用要做什么，例如"写入 notes.md（120 字节）"。
	// 它显示在权限确认弹窗和工具卡片标题上。为什么让工具自己写：只有工具知道哪些参数是关键信息。
	Summarize(input json.RawMessage) string
	// Execute 执行工具。实现必须尊重 ctx 取消（尤其是耗时操作）。
	Execute(ctx context.Context, call Call) Result
}

// Registry 是工具注册表，按注册顺序保存工具（顺序会影响发给模型的工具列表，保持稳定有利于提示缓存）。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string
}

func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register 注册工具。工具名重复会报错，因为模型只能按名字区分工具。
func (r *Registry) Register(t Tool) error {
	name := t.Spec().Name
	if name == "" {
		return fmt.Errorf("tool: empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.tools[name]; dup {
		return fmt.Errorf("tool: duplicate tool %q", name)
	}
	r.tools[name] = t
	r.order = append(r.order, name)
	return nil
}

// MustRegister 用于启动时注册内置工具，失败即编程错误。
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Specs 按注册顺序返回所有工具规格。
func (r *Registry) Specs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name].Spec())
	}
	return out
}
