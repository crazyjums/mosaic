package provider

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// 厂商配置档的网络代理。
//
// 为什么需要：Go 默认只读 HTTP_PROXY / HTTPS_PROXY 环境变量，而从开始菜单或 Finder 启动的桌面应用拿不到
// 终端里设置的变量，国内访问海外厂商时没有地方可配；而国内厂商又不应该走代理。
//
// 配置档的 options.proxy 取值：
//   - 空：沿用环境变量（和以前一致）；设置里的"全局默认代理"在装配配置档时已经填进来了；
//   - "direct"：强制直连，忽略环境变量；
//   - 一个地址：http://、https://、socks5://、socks5h://（可带 user:pass@）。

// ProxyDirect 表示强制直连。
const ProxyDirect = "direct"

// ValidateProxy 检查代理设置是否合法。空串和 "direct" 都合法。
func ValidateProxy(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == ProxyDirect {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("代理地址 %q 不合法，应类似 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080", RedactProxy(raw))
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return nil
	}
	return fmt.Errorf("不支持的代理协议 %q，只支持 http、https、socks5", u.Scheme)
}

// RedactProxy 去掉代理地址里的密码，用于界面显示和日志。无法解析时原样返回的话可能泄露，所以返回固定占位。
func RedactProxy(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == ProxyDirect {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "（无效的代理地址）"
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "***")
		}
	}
	return u.String()
}

// proxyOf 取出配置档的代理设置。
func proxyOf(cfg ProfileConfig) string {
	s, _ := cfg.Options["proxy"].(string)
	return strings.TrimSpace(s)
}

// NewHTTPClient 为配置档创建 HTTP 客户端：代理、拨号和 TLS 握手超时。
// 不设整体超时：流式响应可能持续好几分钟，超时与取消统一交给 ctx。
func NewHTTPClient(cfg ProfileConfig) (*http.Client, error) {
	return NewHTTPClientWithProxy(proxyOf(cfg))
}

// NewHTTPClientWithProxy 同 NewHTTPClient，直接给出代理设置。
func NewHTTPClientWithProxy(proxyURL string) (*http.Client, error) {
	if err := ValidateProxy(proxyURL); err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 15 * time.Second
	switch {
	case proxyURL == "":
		// 沿用 DefaultTransport 的 ProxyFromEnvironment。
	case proxyURL == ProxyDirect:
		tr.Proxy = nil
	default:
		u, _ := url.Parse(proxyURL) // ValidateProxy 已经检查过
		if strings.HasPrefix(u.Scheme, "socks5") {
			d, err := xproxy.FromURL(u, &net.Dialer{Timeout: 15 * time.Second})
			if err != nil {
				return nil, fmt.Errorf("代理 %s: %w", RedactProxy(proxyURL), err)
			}
			tr.Proxy = nil
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if cd, ok := d.(xproxy.ContextDialer); ok {
					return cd.DialContext(ctx, network, addr)
				}
				return d.Dial(network, addr)
			}
		} else {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr}, nil
}
