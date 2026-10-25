package builtin

import (
	"regexp"
	"strings"
)

// 危险命令黑名单：即使用户开了"自动允许"模式，这些命令也直接拒绝。
//
// 这不是安全边界（换个写法总能绕过），目的是拦住模型"手滑"——例如清理目录时写出 rm -rf /，
// 或者照抄网页上的 curl | sh。真正需要时让用户自己在终端里执行。
var dangerousPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`\brm\s+(-[a-zA-Z]*\s+)*-[a-zA-Z]*[rR][a-zA-Z]*\s+(-[a-zA-Z]+\s+)*(--no-preserve-root\s+)?(/|~|\$HOME|/\*|~/\*|\*)(\s|;|&|\||$)`), "递归删除根目录、家目录或当前目录下的全部文件"},
	{regexp.MustCompile(`--no-preserve-root`), "rm --no-preserve-root"},
	{regexp.MustCompile(`\bmkfs(\.\w+)?\b`), "格式化磁盘"},
	{regexp.MustCompile(`(?i)\bformat(\.com)?\s+[a-z]:`), "格式化磁盘"},
	{regexp.MustCompile(`\bdd\s+[^|;&]*\bof=/dev/`), "直接写入块设备"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|hd|disk)\w*`), "直接写入块设备"},
	{regexp.MustCompile(`(?i)(^|[\s;&|])(shutdown|reboot|halt|poweroff)(\s|;|&|\||$)`), "关机或重启"},
	{regexp.MustCompile(`(?i)\bStop-Computer\b|\bRestart-Computer\b`), "关机或重启"},
	{regexp.MustCompile(`\bgit\s+push\b[^;&|]*(\s--force\b|\s-f\b|\s--force-with-lease\b|\s\+\S)`), "git push --force 会覆盖远程历史"},
	{regexp.MustCompile(`\b(curl|wget)\b[^|;&]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`), "下载脚本直接交给 shell 执行（curl | sh）"},
	{regexp.MustCompile(`(?i)\b(iwr|irm|Invoke-WebRequest|Invoke-RestMethod)\b[^|;]*\|\s*iex\b`), "下载脚本直接执行（iwr | iex）"},
	{regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\}\s*;\s*:`), "fork 炸弹"},
	{regexp.MustCompile(`\bchmod\s+(-R\s+)?[0-7]*777\s+/(\s|$)`), "修改根目录权限"},
}

// dangerousCommand 返回命令被拒绝的原因；安全时返回空串。
func dangerousCommand(cmd string) string {
	c := strings.TrimSpace(cmd)
	for _, p := range dangerousPatterns {
		if p.re.MatchString(c) {
			return p.reason
		}
	}
	return ""
}
