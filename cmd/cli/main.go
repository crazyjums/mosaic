package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/bootstrap"
	"github.com/crazyjums/myllmapp/internal/config"
	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/settings"
	"github.com/crazyjums/myllmapp/internal/skill"
)

type flags struct {
	config, provider, model, workspace, mode, resume, prompt string
	yes, jsonOut, noStore, list, demo                        bool
}

func main() {
	var f flags
	flag.StringVar(&f.config, "config", "", "配置文件路径（默认查找 ./config.toml、~/.myllmapp/config.toml）")
	flag.StringVar(&f.provider, "provider", "", "厂商配置档名称，覆盖配置中的 default_provider")
	flag.StringVar(&f.model, "model", "", "模型名称")
	flag.StringVar(&f.workspace, "workspace", "", "工作区目录，覆盖配置")
	flag.StringVar(&f.mode, "mode", "", "权限模式：readonly / default / accept_edits / auto")
	flag.StringVar(&f.resume, "resume", "", "恢复指定 ID 的会话")
	flag.StringVar(&f.prompt, "p", "", "单次执行：发送这条消息，结束后退出")
	flag.BoolVar(&f.yes, "yes", false, "自动允许所有权限确认（谨慎使用）")
	flag.BoolVar(&f.jsonOut, "json", false, "以 JSONL 输出原始事件（提示信息走 stderr）")
	flag.BoolVar(&f.noStore, "no-store", false, "不保存会话到数据库")
	flag.BoolVar(&f.demo, "demo", false, "使用演示厂商 fake（按固定剧本回复，不需要密钥、不调用真实模型）")
	flag.BoolVar(&f.list, "sessions", false, "列出已保存的会话后退出")
	flag.Parse()

	if err := run(f); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(f flags) error {
	// 步骤 1：读配置并组装内核。这两步与桌面端完全一致。
	cfg, err := config.Load(f.config)
	if err != nil {
		return err
	}
	if f.demo {
		cfg.EnableDemo()
	}
	built, err := bootstrap.Build(cfg, bootstrap.Options{NoStore: f.noStore, PermissionMode: f.mode, Workspace: f.workspace})
	if err != nil {
		return err
	}
	defer built.Close()
	eng := built.Engine

	if f.provider != "" || f.model != "" {
		profile := f.provider
		if profile == "" {
			profile = eng.Status().Provider
		}
		if err := eng.SetModel(profile, f.model); err != nil {
			return err
		}
	}
	if f.list {
		return listSessions(eng, os.Stdout)
	}

	// 步骤 2：接上"界面"。CLI 有两种界面：人类可读的渲染器，或原样输出 JSONL。
	ui := os.Stdout
	info := io.Writer(os.Stderr)
	if !f.jsonOut {
		info = os.Stdout
	}
	interactive := isTerminal(os.Stdin)
	permCh := make(chan event.PermissionRequest, 4)
	eng.Bus().Subscribe(func(ev event.Event) {
		if f.jsonOut {
			b, _ := json.Marshal(ev)
			fmt.Fprintln(ui, string(b))
		}
		// 权限请求：-yes 时立即同意（Broker 先登记后通知，所以在回调里同步回复是安全的）；
		// 否则交给主循环去读用户输入。回调里不能阻塞，所以用带缓冲的 channel。
		if p, ok := ev.Data.(event.PermissionRequest); ok {
			if f.yes {
				_ = eng.RespondPermission(p.RequestID, "allow")
			} else {
				permCh <- p
			}
		}
	})
	if !f.jsonOut {
		r := &renderer{out: ui, c: newPalette(), echoUser: !interactive}
		eng.Bus().Subscribe(r.handle)
	}

	if f.resume != "" {
		if _, err := eng.LoadSession(context.Background(), f.resume); err != nil {
			return err
		}
	}

	// 步骤 3：输入循环。stdin 由一个 goroutine 读取，主循环用 select 同时等待"输入、权限请求、Ctrl-C、本轮结束"。
	c := &cli{eng: eng, settings: built.Settings, lines: readLines(os.Stdin), perm: permCh, info: info, interactive: interactive}
	c.sig = make(chan os.Signal, 1)
	signal.Notify(c.sig, os.Interrupt)

	st := eng.Status()
	fmt.Fprintf(info, "myllmapp CLI · %s/%s · 工作区 %s · 配置 %s\n", st.Provider, st.Model, built.Workspace, orDefault(cfg.Source, "（内置默认）"))
	if f.prompt != "" {
		c.turn(f.prompt)
		return nil
	}
	if interactive {
		fmt.Fprintln(info, "输入消息开始对话，/help 查看命令，Ctrl-C 取消当前轮，Ctrl-D 退出。")
	}
	return c.loop()
}

type cli struct {
	eng         *core.Engine
	settings    *settings.Manager
	lines       <-chan string
	perm        <-chan event.PermissionRequest
	sig         chan os.Signal
	info        io.Writer
	interactive bool
}

func (c *cli) loop() error {
	for {
		if c.interactive {
			fmt.Fprint(c.info, "\n› ")
		}
		select {
		case <-c.sig:
			fmt.Fprintln(c.info)
			return nil
		case line, ok := <-c.lines:
			if !ok {
				return nil
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "/") {
				if quit := c.command(line); quit {
					return nil
				}
				continue
			}
			c.turn(line)
		}
	}
}

// turn 运行一轮对话。Engine.Send 是阻塞的，所以放到 goroutine 里，主 goroutine 负责响应权限请求和 Ctrl-C。
func (c *cli) turn(text string) {
	done := make(chan error, 1)
	go func() {
		_, err := c.eng.Send(context.Background(), text)
		done <- err
	}()
	for {
		select {
		case err := <-done:
			if err != nil {
				fmt.Fprintln(c.info, "错误:", err)
			}
			return
		case <-c.sig:
			fmt.Fprintln(c.info, "\n正在取消…")
			c.eng.Cancel()
		case p := <-c.perm:
			c.askPermission(p, done)
		}
	}
}

// askPermission 等待用户对一个权限请求的回答。等待期间如果本轮结束（被取消）就放弃。
func (c *cli) askPermission(p event.PermissionRequest, done chan error) {
	fmt.Fprint(c.info, "   允许吗？[y] 允许一次  [a] 本会话总是允许  [n] 拒绝 › ")
	select {
	case line, ok := <-c.lines:
		decision := "deny"
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes", "":
			decision = "allow"
		case "a", "always":
			decision = "allow_session"
		}
		if !ok {
			decision = "deny" // 输入已结束（管道读完），保守地拒绝
		}
		if !c.interactive {
			fmt.Fprintln(c.info, decision)
		}
		if err := c.eng.RespondPermission(p.RequestID, decision); err != nil {
			fmt.Fprintln(c.info, "回复失败:", err)
		}
	case <-c.sig:
		fmt.Fprintln(c.info, "\n正在取消…")
		c.eng.Cancel()
	case err := <-done:
		// 本轮已结束，把结果放回去让 turn 的循环读到。
		done <- err
	}
}

func (c *cli) command(line string) (quit bool) {
	fields := strings.Fields(line)
	ctx := context.Background()
	switch fields[0] {
	case "/quit", "/exit":
		return true
	case "/help":
		fmt.Fprintln(c.info, `/new                      新会话
/sessions                 列出已保存的会话
/load <id>                恢复会话
/models                   列出厂商配置档和模型
/model <profile> [model]  切换厂商和模型（会记住，下次启动直接使用）
/mode [mode]              查看或切换权限模式：readonly / default / accept_edits / auto
/status                   当前状态
/cd [dir]                 查看或切换当前会话的工作区（/cd - 改回默认工作区）
/skills                   列出可用的技能（输入 /技能名 开头的消息即可使用）
/login [new]              用 ChatGPT 账号登录（使用套餐额度）；new 表示换一个账号
/logout                   退出 ChatGPT 账号
/quit                     退出`)
	case "/new":
		if st, err := c.eng.NewSession(); err != nil {
			fmt.Fprintln(c.info, "错误:", err)
		} else {
			fmt.Fprintln(c.info, "新会话", st.SessionID)
		}
	case "/sessions":
		if err := listSessions(c.eng, c.info); err != nil {
			fmt.Fprintln(c.info, "错误:", err)
		}
	case "/load":
		if len(fields) < 2 {
			fmt.Fprintln(c.info, "用法：/load <id>")
			return false
		}
		if _, err := c.eng.LoadSession(ctx, fields[1]); err != nil {
			fmt.Fprintln(c.info, "错误:", err)
		}
	case "/models":
		for _, p := range c.eng.Profiles() {
			state := "可用"
			if !p.Available {
				state = "不可用：" + p.Error
			}
			fmt.Fprintf(c.info, "%-12s %-10s %v  (%s)\n", p.Name, p.Type, p.Models, state)
		}
	case "/model":
		if len(fields) < 2 {
			fmt.Fprintln(c.info, "用法：/model <profile> [model]")
			return false
		}
		model := ""
		if len(fields) > 2 {
			model = fields[2]
		}
		// 通过 settings 切换：和桌面端一样会记住选择，下次启动直接使用。
		if err := c.settings.Select(fields[1], model); err != nil {
			fmt.Fprintln(c.info, "错误:", err)
			return false
		}
		st := c.eng.Status()
		fmt.Fprintf(c.info, "已切换到 %s/%s\n", st.Provider, st.Model)
	case "/mode":
		if len(fields) < 2 {
			fmt.Fprintf(c.info, "当前权限模式：%s（可选 readonly / default / accept_edits / auto）\n", c.eng.Status().PermissionMode)
			return false
		}
		// 通过 settings 切换：和桌面端一样会记住（auto 除外）。
		if err := c.settings.SetPermissionMode(fields[1]); err != nil {
			fmt.Fprintln(c.info, "错误:", err)
			return false
		}
		if fields[1] == "auto" {
			fmt.Fprintln(c.info, "⚠ 已切换到 auto：写文件和执行命令都不再询问，请只在可以随意破坏的目录里使用（只对本次运行有效）。")
		} else {
			fmt.Fprintln(c.info, "已切换到权限模式", fields[1])
		}
	case "/cd":
		if len(fields) < 2 {
			st := c.eng.Status()
			note := ""
			if st.WorkspaceIsDefault {
				note = "（默认工作区）"
			}
			fmt.Fprintf(c.info, "当前会话的工作区：%s%s\n", st.Workspace, note)
			return false
		}
		dir := strings.TrimSpace(strings.TrimPrefix(line, "/cd"))
		if dir == "-" {
			dir = ""
		}
		st, err := c.settings.SetSessionWorkspace(ctx, dir)
		if err != nil {
			fmt.Fprintln(c.info, "错误:", err)
			return false
		}
		fmt.Fprintln(c.info, "当前会话的工作区：", st.Workspace)
	case "/login":
		c.loginChatGPT(len(fields) > 1 && fields[1] == "new")
	case "/logout":
		if err := c.settings.LogoutChatGPT(ctx); err != nil {
			fmt.Fprintln(c.info, "提示:", err)
		}
		fmt.Fprintln(c.info, "已退出 ChatGPT 账号")
	case "/skills":
		home, _ := os.UserHomeDir()
		list := skill.Discover(c.eng.Status().Workspace, home)
		if len(list) == 0 {
			fmt.Fprintln(c.info, "没有找到技能。把技能文件夹（里面有 SKILL.md）放到 <工作区>/.myllmapp/skills、<工作区>/.claude/skills、~/.myllmapp/skills 或 ~/.claude/skills 即可。")
		}
		for _, s := range list {
			fmt.Fprintf(c.info, "/%s  %s  [%s · %s]\n", s.Name, s.Description, s.Source, s.Origin)
		}
	case "/status":
		st := c.eng.Status()
		fmt.Fprintf(c.info, "会话 %s「%s」\n厂商 %s · 模型 %s\ntokens 入 %d / 出 %d\n工作区 %s\n权限模式 %s\n",
			st.SessionID, st.Title, st.Provider, st.Model, st.Usage.InputTokens, st.Usage.OutputTokens, st.Workspace, st.PermissionMode)
	default:
		fmt.Fprintln(c.info, "未知命令，/help 查看帮助")
	}
	return false
}

// loginChatGPT 打印授权地址，等待用户在浏览器里完成登录（Ctrl+C 取消）。
// 回调服务器开在本机 127.0.0.1 上，所以浏览器必须和 CLI 在同一台电脑上。
func (c *cli) loginChatGPT(newAccount bool) {
	u, err := c.settings.LoginChatGPT(newAccount)
	if err != nil {
		fmt.Fprintln(c.info, "错误:", err)
		return
	}
	fmt.Fprintf(c.info, "请在浏览器中打开下面的地址完成登录（Ctrl+C 取消）：\n\n  %s\n\n", u)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-c.sig:
			c.settings.CancelChatGPTLogin()
			fmt.Fprintln(c.info, "已取消登录")
			return
		case <-tick.C:
		}
		st := c.settings.ChatGPT()
		switch st.State {
		case "pending":
			continue
		case "logged_in":
			fmt.Fprintf(c.info, "已登录 %s。用 /models 查看可用模型，/model chatgpt <模型> 切换。\n", st.Email)
			if st.ModelsError != "" {
				fmt.Fprintln(c.info, "提示:", st.ModelsError)
			}
		default:
			fmt.Fprintln(c.info, "登录没有完成:", st.Error)
		}
		return
	}
}

func listSessions(eng *core.Engine, w io.Writer) error {
	list, err := eng.ListSessions(context.Background())
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(w, "（还没有保存的会话）")
	}
	for _, s := range list {
		fmt.Fprintf(w, "%s  %s  %-24s %3d 条  %s/%s\n", s.ID, s.UpdatedAt.Format("01-02 15:04"), s.Title, s.MessageCount, s.Provider, s.Model)
	}
	return nil
}

// readLines 在后台逐行读取输入，读完后关闭 channel。
func readLines(r io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if line != "" || err == nil {
				ch <- strings.TrimRight(line, "\r\n")
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					fmt.Fprintln(os.Stderr, "读取输入失败:", err)
				}
				return
			}
		}
	}()
	return ch
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
