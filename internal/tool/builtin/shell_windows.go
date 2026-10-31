//go:build windows

package builtin

import (
	"context"
	"os/exec"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/crazyjums/myllmapp/internal/sysproc"
)

// prepareCmd 在 Windows 上不为命令创建控制台窗口（见 internal/sysproc），取消时只结束 cmd 进程。
// 完整的进程树清理需要 Job Object，第一阶段不做。
func prepareCmd(cmd *exec.Cmd) {
	sysproc.HideConsole(cmd)
}

// newShellCmd 用 cmd /S /C 运行命令，并且**原样**传递命令行。
//
// 为什么不用 exec.Command("cmd", "/C", command)：Go 在 Windows 上会按 CommandLineToArgvW 的规则给参数加引号、
// 把里面的 " 转义成 \"，而 cmd.exe 不认识 \"。结果是带引号的命令全被破坏——findstr "a b" 变成找 a" 和 b"，
// powershell -Command "..." 只会把命令文本原样打印出来，模型怎么改引号都不对。
// 所以直接设置 SysProcAttr.CmdLine；/S 让 cmd 只去掉最外面这一对引号，里面的内容原样执行。
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + command + `"`}
	return cmd
}

// decodeOutput 把命令输出转成 UTF-8。cmd 内置命令和很多程序按系统的 OEM 代码页输出（中文系统是 GBK），
// 直接当 UTF-8 用就是满屏乱码，模型看不懂错误信息，只会瞎猜。逐行判断：本来就是合法 UTF-8 的行（git、node 等）
// 原样保留，其余的行按 OEM 代码页解码。
func decodeOutput(b []byte) string { return decodeLines(b, oemToUTF8) }

var procMultiByteToWideChar = syscall.NewLazyDLL("kernel32.dll").NewProc("MultiByteToWideChar")

const cpOEMCP = 1

func oemToUTF8(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n, _, _ := procMultiByteToWideChar.Call(cpOEMCP, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return string(b)
	}
	w := make([]uint16, n)
	if r, _, _ := procMultiByteToWideChar.Call(cpOEMCP, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&w[0])), n); r == 0 {
		return string(b)
	}
	return string(utf16.Decode(w))
}

func shellName() string { return "Windows cmd.exe" }

// shellHint 提醒模型这是 cmd.exe 而不是 bash 或 PowerShell：不然它会在三种语法之间乱试，把步数都耗在失败的命令上。
func shellHint() string {
	return "这是 Windows 的 cmd.exe，不是 bash 也不是 PowerShell：用 dir、type、findstr、where，用 & 或 && 连接命令，不要用 ls、grep、cat、$变量、command -v。需要 PowerShell 时写 powershell -NoProfile -Command \"...\"。输出里的中文已经转成 UTF-8。"
}
