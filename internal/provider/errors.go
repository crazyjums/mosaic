package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 错误分类，是故障转移的依据：哪些失败换一个成员重试有意义，哪些换谁都一样会失败。
//
// 适配器返回的错误原本只是一段文字，调用方没法判断是"限流"还是"请求本身有问题"。
// 这里让适配器把 HTTP 状态码、Retry-After 等信息包进 HTTPError，由 Classify 统一分类。

// ErrorKind 是错误的类别。
type ErrorKind string

const (
	KindNone      ErrorKind = ""
	KindRateLimit ErrorKind = "rate_limit" // 429
	KindServer    ErrorKind = "server"     // 5xx、529 过载
	KindAuth      ErrorKind = "auth"       // 401 / 403
	KindNetwork   ErrorKind = "network"    // 连接失败、超时、流被中断
	KindContext   ErrorKind = "context"    // 上下文超长
	KindQuota     ErrorKind = "quota"      // 额度 / 余额用完
	KindCancelled ErrorKind = "cancelled"  // 用户取消
	KindOther     ErrorKind = "other"      // 其他（含请求本身有问题的 400 / 422）：不换成员
)

// Failover 说明这类错误是否应该换下一个成员重试。
// "其他"默认不换：无法识别的错误宁可直接报出来，也不要把一个请求本身的 bug 在每个成员上都撞一遍。
func (k ErrorKind) Failover() bool {
	switch k {
	case KindRateLimit, KindServer, KindAuth, KindNetwork, KindContext, KindQuota:
		return true
	}
	return false
}

// ErrQuota 表示额度 / 余额用完。适配器用 %w 包装它（例如 chatgpt.ErrUsageLimit），Classify 据此识别。
var ErrQuota = errors.New("quota exhausted")

type quotaError struct{ msg string }

func (e *quotaError) Error() string { return e.msg }
func (e *quotaError) Unwrap() error { return ErrQuota }

// NewQuotaError 创建一个"额度用完"错误：显示 msg 原文（不带多余前缀），同时 errors.Is(err, ErrQuota) 为真。
func NewQuotaError(msg string) error { return &quotaError{msg: msg} }

// ErrStreamInterrupted 表示流在正常结束前被中断（连接被掉线、服务端提前关闭）。
var ErrStreamInterrupted = errors.New("stream interrupted")

// ErrFirstByteTimeout 表示等待首个响应事件超时（模型组里设置的"首字节超时"）。
var ErrFirstByteTimeout = errors.New("timed out waiting for the first response event")

// HTTPError 是带 HTTP 状态码的厂商错误。Error() 返回适配器原来的文字，不改变既有的提示。
type HTTPError struct {
	Status     int
	RetryAfter time.Duration
	Err        error
}

func (e *HTTPError) Error() string { return e.Err.Error() }
func (e *HTTPError) Unwrap() error { return e.Err }

// NewHTTPError 用响应和已经整理好的错误创建 HTTPError，解析 Retry-After。
func NewHTTPError(resp *http.Response, err error) *HTTPError {
	return &HTTPError{Status: resp.StatusCode, RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After")), Err: err}
}

// ParseRetryAfter 解析 Retry-After 头：秒数，或一个 HTTP 日期。无法解析返回 0。
func ParseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// contextHints 是各家"上下文超长"错误信息里常见的字样（小写比较）。
var contextHints = []string{
	"context_length_exceeded", "context length", "maximum context", "prompt is too long",
	"too many tokens", "exceeds the context window", "input is too long", "input too long",
	"上下文", "超出最大长度",
}

// quotaHints 是"余额 / 额度用完"的常见字样。
var quotaHints = []string{"insufficient_quota", "insufficient balance", "exceeded your current quota", "billing", "余额不足", "欠费", "quota exceeded"}

func hasAny(s string, hints []string) bool {
	s = strings.ToLower(s)
	for _, h := range hints {
		if strings.Contains(s, h) {
			return true
		}
	}
	return false
}

// Classify 把适配器返回的错误分类。nil 返回 KindNone。
func Classify(err error) ErrorKind {
	if err == nil {
		return KindNone
	}
	if errors.Is(err, context.Canceled) {
		return KindCancelled
	}
	if errors.Is(err, ErrQuota) {
		return KindQuota
	}
	if errors.Is(err, ErrFirstByteTimeout) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrStreamInterrupted) {
		return KindNetwork
	}
	var he *HTTPError
	if errors.As(err, &he) {
		msg := err.Error()
		switch {
		case he.Status == http.StatusRequestEntityTooLarge:
			return KindContext
		case he.Status == http.StatusPaymentRequired:
			return KindQuota
		case he.Status == http.StatusTooManyRequests:
			if hasAny(msg, quotaHints) {
				return KindQuota
			}
			return KindRateLimit
		case he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden:
			return KindAuth
		case he.Status == http.StatusRequestTimeout || he.Status >= 500:
			return KindServer
		case he.Status == http.StatusBadRequest || he.Status == http.StatusUnprocessableEntity:
			if hasAny(msg, contextHints) {
				return KindContext
			}
			if hasAny(msg, quotaHints) {
				return KindQuota
			}
			return KindOther
		}
		return KindOther
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return KindNetwork
	}
	return KindOther
}
