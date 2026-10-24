// Command cli 是 harness 的终端入口：用和桌面端完全相同的内核在终端里跑对话，方便调试。
//
// # 负责什么
//
//   - 解析命令行参数 → internal/config 读配置 → internal/bootstrap 组装 Engine；
//   - 订阅事件总线，把事件渲染成彩色终端输出（render.go），或用 --json 原样输出 JSONL；
//   - 读取用户输入、回答权限确认、处理 Ctrl-C 取消（main.go）。
//
// # 不负责什么
//
// 不包含任何 agent 逻辑。它和 internal/app（Wails 绑定层）是平级的两个"界面"，
// 都只通过 core.Engine 的命令和事件与内核交互——这正是"内核与界面解耦"的验证。
//
// # 常用方式
//
//	go run ./cmd/cli                                  # 交互模式（默认 fake 厂商的演示剧本）
//	go run ./cmd/cli -p "列出当前目录"                  # 单次执行
//	printf '演示\ny\ny\n' | go run ./cmd/cli          # 管道输入：第一行是问题，之后的 y 回答权限确认
//	go run ./cmd/cli -json -yes -p hi > events.jsonl  # 导出原始事件流
//
// 交互模式下的命令：/help /new /sessions /load <id> /models /model <profile> [model] /status /quit
package main
