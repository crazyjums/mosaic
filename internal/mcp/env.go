package mcp

import (
	"os"
	"strings"
)

// 插件带来的子进程只继承这些基础环境变量（再加上清单里声明的），而不是应用自己的整个环境：
// 应用的环境里可能有厂商的 API Key 等密钥，一个第三方插件没有理由看到它们。
//
// 名字比较不区分大小写（Windows 的环境变量名不区分大小写）。
var baseEnvNames = []string{
	// 找得到程序、有家目录
	"PATH", "PATHEXT", "HOME", "USERPROFILE", "USER", "USERNAME", "LOGNAME",
	// 语言和终端
	"LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE", "TERM", "TZ",
	// 临时目录
	"TMPDIR", "TEMP", "TMP",
	// Windows 系统目录（很多程序没有它们起不来）
	"SystemRoot", "SystemDrive", "windir", "ComSpec", "APPDATA", "LOCALAPPDATA", "ProgramData", "ProgramFiles", "ProgramFiles(x86)",
	// Linux 桌面：钥匙串（Secret Service）和配置目录的位置
	"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
	// 网络代理和证书（公司网络里没有它们连不上外网）
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR",
}

// BaseEnv 返回基础环境变量（"名=值"）：只包含白名单里当前进程里确实存在的那些。
func BaseEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		for _, allowed := range baseEnvNames {
			if strings.EqualFold(name, allowed) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// IsPluginSource 报告来源是不是插件。
func IsPluginSource(source string) bool { return strings.HasPrefix(source, "plugin:") }
