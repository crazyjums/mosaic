// Package sysproc 统一处理"桌面应用启动命令行程序"时的平台差异。
//
// 问题：在 Windows 上，图形界面程序启动控制台程序（git、cmd、python……）时，系统会为它新建一个控制台窗口，
// 表现为"一个黑色窗口闪一下又消失"。状态栏每 15 秒查一次 git 分支、切换权限时重新运行状态栏脚本、
// agent 执行命令、连接 MCP 服务器，都会触发。
//
// 只设置 SysProcAttr.HideWindow 不够：它只是请求"窗口创建后隐藏"，在 Windows 11 默认终端是
// Windows Terminal 时会被忽略，窗口照样闪一下。CREATE_NO_WINDOW 标志则让进程根本不创建控制台窗口
// （进程仍有一个不可见的控制台，子进程会继承它，所以 cmd 里再启动的程序也不会弹窗）。
//
// 所有 exec.Command 在启动前都应调用 HideConsole；其他平台上它什么都不做。
package sysproc
