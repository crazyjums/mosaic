package mcp

import (
	"os"
	"regexp"
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

// 工具链透传：让 npx / uvx / pip / cargo / go 这类包管理器在公司代理、自签证书、国内镜像的环境里还能联网。
// 没有它们，插件的 npx 服务器会卡到握手超时（复现过：自签证书代理下 npm 报 SELF_SIGNED_CERT_IN_CHAIN）。
// 按名字规则放行，但名字里带凭据字样的一律剔除（例如 npm_config__authToken）；
// NODE_OPTIONS、PYTHONPATH、LD_PRELOAD 这类能改变程序加载什么代码的变量不放行。
var toolchainPrefixes = []string{"npm_config_", "UV_", "PIP_", "CARGO_", "GOPROXY", "GONOSUMDB", "GONOPROXY", "GOPRIVATE", "GOFLAGS"}

var toolchainExact = []string{"NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "NIX_SSL_CERT_FILE"}

var credentialWords = []string{"token", "auth", "password", "passwd", "secret", "key", "credential", "cookie"}

// isToolchainEnv 报告这个名字是不是可以透传给插件子进程的工具链变量。
func isToolchainEnv(name string) bool {
	lower := strings.ToLower(name)
	for _, w := range credentialWords {
		if strings.Contains(lower, w) {
			return false
		}
	}
	for _, n := range toolchainExact {
		if strings.EqualFold(name, n) {
			return true
		}
	}
	for _, p := range toolchainPrefixes {
		if strings.HasPrefix(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// urlUserinfoRe 匹配 URL 里内嵌的凭据（scheme://user:password@host 或 scheme://token@host）。
var urlUserinfoRe = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^/\s@]+@`)

// hasEmbeddedCredential 报告值里是不是带了内嵌凭据的 URL。名字里没有 token / password 字样的变量，
// 值也可能带凭据（例如 PIP_INDEX_URL=https://user:token@packages.example/simple）：这种整个变量不传——
// 插件需要私有仓库的话，应该在自己的清单里声明它要的环境变量，而不是继承用户的。
func hasEmbeddedCredential(value string) bool { return urlUserinfoRe.MatchString(value) }

// BaseEnv 返回基础环境变量（"名=值"）：只包含白名单里当前进程里确实存在的那些。
func BaseEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if isToolchainEnv(name) {
			// 工具链变量按名字规则放行，所以还要看值：带内嵌凭据的 URL 不传。
			if _, value, _ := strings.Cut(kv, "="); !hasEmbeddedCredential(value) {
				out = append(out, kv)
			}
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
