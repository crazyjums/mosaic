package core

import "context"

// 本文件是"后台任务"的扩展点（第一阶段只有接口）。
//
// 后台任务是"agent 发起、但不阻塞 agent 循环"的长时间操作，例如启动 dev server、跑一个很久的测试。
// 与 shell 工具（同步执行、有超时）的区别在于：任务立即返回 task_id，输出通过 task_log 事件持续推送，
// agent 之后可以用另一个工具查询任务状态或读取日志。
//
// 计划的接入方式（详见 docs/roadmap.md）：
//   - 实现 TaskManager（例如基于 os/exec + 环形缓冲区保存最近日志）；
//   - 提供 start_task / task_status / stop_task 三个工具，内部调用 TaskManager；
//   - TaskManager 通过 event.Publisher 发 task_start / task_log / task_end，带上 session_id 与 agent_id；
//   - 前端已经能识别这些事件类型（第一阶段渲染为简单的任务面板占位）。

// TaskSpec 描述要启动的后台任务。
type TaskSpec struct {
	SessionID string
	AgentID   string
	Title     string
	Kind      string // 例如 "shell"
	Command   string
}

// TaskInfo 是任务的当前状态。
type TaskInfo struct {
	ID       string
	Title    string
	Status   string // running / completed / failed / cancelled
	ExitCode int
}

// TaskManager 管理后台任务的生命周期。
type TaskManager interface {
	Start(ctx context.Context, spec TaskSpec) (taskID string, err error)
	Stop(taskID string) error
	Get(taskID string) (TaskInfo, bool)
	List(sessionID string) []TaskInfo
	// Logs 返回任务最近的输出（用于 agent 查询），tail 为最多返回的字节数。
	Logs(taskID string, tail int) (string, error)
}
