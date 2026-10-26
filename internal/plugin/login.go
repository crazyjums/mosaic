package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// 一键登录（Manifest.Login）：用户点一个按钮 → 应用运行 Start 拿到授权链接并在浏览器里打开 → 后台运行 Finish
// 等用户在浏览器里授权完成 → 界面轮询状态。
//
// 为什么应用只负责“开浏览器和等待”：令牌怎么换、存在哪都是插件自己的程序（比如 lark-cli）的事，
// 这里不接触任何令牌；链接只能是 https 且域名在插件声明的白名单里，插件的程序不能借登录让应用打开任意网址。

const (
	loginStartTimeout  = 40 * time.Second
	loginFinishTimeout = 10 * time.Minute
	maxLoginMessage    = 1500
)

// 登录的状态。
const (
	LoginIdle      = "idle"
	LoginRunning   = "running"
	LoginOK        = "ok"
	LoginFailed    = "failed"
	LoginCancelled = "cancelled"
)

// LoginView 是详情里“登录”按钮的描述。
type LoginView struct {
	Label string   `json:"label"`
	Hosts []string `json:"hosts"`
}

// LoginState 是一次登录的当前状态。
type LoginState struct {
	State string `json:"state"`
	// URL 是授权链接（浏览器没有自动打开时，界面上让用户点开或复制）。
	URL string `json:"url,omitempty"`
	// Message 是给用户看的一句话；失败时是原因。
	Message string `json:"message,omitempty"`
}

type loginRun struct {
	state   LoginState
	cancel  context.CancelFunc
	started time.Time
}

// StartLogin 开始登录并返回授权链接（调用方负责在浏览器里打开它）。已经有一次登录在等待时直接返回它的状态，
// 方便用户重新打开链接。
func (h *Host) StartLogin(id string) (LoginState, error) {
	h.mu.Lock()
	l, ok := h.plugins[id]
	if !ok {
		h.mu.Unlock()
		return LoginState{}, fmt.Errorf("没有安装插件 %q", id)
	}
	if l.m.Login == nil {
		h.mu.Unlock()
		return LoginState{}, errors.New("这个插件没有登录流程")
	}
	if run := h.logins[id]; run != nil && run.state.State == LoginRunning {
		st := run.state
		h.mu.Unlock()
		return st, nil
	}
	spec := *l.m.Login
	h.mu.Unlock()

	if miss := h.missing(l.m); len(miss) > 0 {
		return LoginState{}, errors.New("缺少依赖：" + strings.Join(miss, "、"))
	}
	argv, dir, env, _, err := h.prepareRun(id, spec.Start)
	if err != nil {
		return LoginState{}, err
	}
	res := runCommand(context.Background(), dir, argv, env, loginStartTimeout)
	if res.Err != nil {
		return LoginState{}, res.Err
	}
	if res.ExitCode != 0 {
		return LoginState{}, fmt.Errorf("没能开始登录：%s", tail(res.Output, maxLoginMessage))
	}
	link, message, err := parseLoginStart(res.Output, spec.URLHosts)
	if err != nil {
		return LoginState{}, err
	}

	fargv, fdir, fenv, _, err := h.prepareRun(id, spec.Finish)
	if err != nil {
		return LoginState{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &loginRun{state: LoginState{State: LoginRunning, URL: link, Message: message}, cancel: cancel, started: time.Now()}
	h.mu.Lock()
	if old := h.logins[id]; old != nil && old.state.State == LoginRunning { // 两次点击赶在一起：保留先到的
		h.mu.Unlock()
		cancel()
		return old.state, nil
	}
	h.logins[id] = run
	h.mu.Unlock()

	go func() {
		r := runCommand(ctx, fdir, fargv, fenv, loginFinishTimeout)
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.logins[id] != run || run.state.State != LoginRunning { // 已经被取消或被新的一次替换
			return
		}
		out := tail(r.Output, maxLoginMessage)
		switch {
		case r.Err != nil:
			run.state = LoginState{State: LoginFailed, URL: link, Message: r.Err.Error()}
		case r.ExitCode != 0:
			run.state = LoginState{State: LoginFailed, URL: link, Message: out}
		default:
			run.state = LoginState{State: LoginOK, Message: out}
		}
		cancel()
	}()
	return run.state, nil
}

// GetLoginState 返回插件当前的登录状态；没有登录过是 idle。
func (h *Host) GetLoginState(id string) LoginState {
	h.mu.Lock()
	defer h.mu.Unlock()
	if run := h.logins[id]; run != nil {
		return run.state
	}
	return LoginState{State: LoginIdle}
}

// CancelLogin 取消正在等待的登录（终止后台进程）。没有进行中的登录时什么也不做。
func (h *Host) CancelLogin(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if run := h.logins[id]; run != nil && run.state.State == LoginRunning {
		run.state = LoginState{State: LoginCancelled, Message: "已取消"}
		run.cancel()
	}
}

// parseLoginStart 解析 Start 的输出：找出第一个 JSON 对象，取 url 和 message，并校验链接。
func parseLoginStart(out string, hosts []string) (link, message string, err error) {
	i := strings.Index(out, "{")
	if i < 0 {
		return "", "", errors.New("插件没有返回授权链接（输出里没有 JSON）")
	}
	var v struct {
		URL     string `json:"url"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(strings.NewReader(out[i:])).Decode(&v); err != nil {
		return "", "", fmt.Errorf("插件返回的授权信息不是合法的 JSON：%v", err)
	}
	if err := checkLoginURL(v.URL, hosts); err != nil {
		return "", "", err
	}
	msg := []rune(strings.TrimSpace(v.Message))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return v.URL, string(msg), nil
}

// checkLoginURL 只放行 https 且域名在白名单里的链接（白名单项可以写 *.example.com，匹配它的所有子域名和它本身）。
func checkLoginURL(raw string, hosts []string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || len(raw) > 4000 {
		return errors.New("插件返回的授权链接不合法（必须是 https 链接）")
	}
	host := strings.ToLower(u.Hostname())
	for _, p := range hosts {
		p = strings.ToLower(p)
		if p == host {
			return nil
		}
		if suffix, ok := strings.CutPrefix(p, "*."); ok && (host == suffix || strings.HasSuffix(host, "."+suffix)) {
			return nil
		}
	}
	return fmt.Errorf("授权链接的域名 %s 不在插件声明的 login.url_hosts 里，出于安全没有打开", host)
}
