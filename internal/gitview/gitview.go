package gitview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/sysproc"
)

// 限制。
const (
	// MaxLogPage 是一次最多返回的提交数。
	MaxLogPage = 500
	// MaxDiffBytes 是一个 diff 最多返回的字节数，超出部分截断并标记 Truncated。
	MaxDiffBytes = 400 * 1024
	// maxListBytes 是列表类输出（log、分支、文件列表）的上限，防止巨型仓库把界面拖垮。
	maxListBytes = 16 * 1024 * 1024

	callTimeout = 20 * time.Second
)

// ErrNotRepo 表示工作区不是 git 仓库（或没装 git）。
var ErrNotRepo = errors.New("工作区不是 git 仓库")

// Repo 是一个要查看的仓库（工作区目录）。
type Repo struct{ Dir string }

var hashRe = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// 字段/记录分隔符：不会出现在提交信息、作者名里的控制字符。
const (
	fs = "\x1f"
	rs = "\x1e"
)

// run 执行一条只读 git 命令，返回 stdout。
func (r Repo) run(ctx context.Context, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	full := append([]string{
		"-c", "core.quotepath=off",
		"-c", "core.fsmonitor=false", // 仓库配置里的 fsmonitor 会被 git status 当作程序执行
		"-c", "diff.external=",
		"--no-pager",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	sysproc.HideConsole(cmd)
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, left: limit}
	cmd.Stderr = &errb
	err := cmd.Run()
	// 输出被我们主动截断时 git 会因管道关闭而失败，这不算错误。
	if lw := cmd.Stdout.(*limitWriter); lw.truncated {
		return out.Bytes(), nil
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, fmt.Errorf("无法运行 git：%v。请确认已安装 git", err)
		}
		msg := strings.TrimSpace(errb.String())
		if strings.Contains(msg, "not a git repository") {
			return nil, ErrNotRepo
		}
		return nil, errors.New(clipStr(msg, 500))
	}
	return out.Bytes(), nil
}

// limitWriter 最多写入 left 字节，超出的丢弃并记录 truncated；写满后返回错误让 git 提前结束。
type limitWriter struct {
	w         *bytes.Buffer
	left      int
	truncated bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.left <= 0 {
		l.truncated = true
		return 0, errors.New("output limit")
	}
	if len(p) > l.left {
		l.w.Write(p[:l.left])
		l.left = 0
		l.truncated = true
		return len(p), errors.New("output limit")
	}
	l.w.Write(p)
	l.left -= len(p)
	return len(p), nil
}

func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- 提交历史 ----

// Ref 是指向某个提交的引用。
type Ref struct {
	Name string `json:"name"`
	// Kind：branch / remote / tag
	Kind string `json:"kind"`
	// Head 表示 HEAD 当前指向它（当前分支）。
	Head bool `json:"head,omitempty"`
}

// Commit 是历史里的一个提交。
type Commit struct {
	Hash    string   `json:"hash"`
	Short   string   `json:"short"`
	Parents []string `json:"parents"`
	Refs    []Ref    `json:"refs,omitempty"`
	Author  string   `json:"author"`
	Time    int64    `json:"time"` // unix 秒
	Subject string   `json:"subject"`
}

const logFormat = "%H" + fs + "%P" + fs + "%D" + fs + "%an" + fs + "%at" + fs + "%s" + rs

// Log 返回按提交时间倒序的所有分支上的提交（git log --all --date-order），用于画分支图。
// skip/limit 用于分页；limit 超过 MaxLogPage 按 MaxLogPage 处理。
func (r Repo) Log(ctx context.Context, skip, limit int) ([]Commit, error) {
	if limit <= 0 || limit > MaxLogPage {
		limit = MaxLogPage
	}
	if skip < 0 {
		skip = 0
	}
	out, err := r.run(ctx, maxListBytes, "log", "--all", "--date-order", "--decorate=full",
		"--format="+logFormat, "-n", strconv.Itoa(limit), "--skip", strconv.Itoa(skip))
	if err != nil {
		// 空仓库（还没有提交）：git log 会报 “does not have any commits yet”
		if strings.Contains(err.Error(), "does not have any commits") || strings.Contains(err.Error(), "unknown revision") {
			return []Commit{}, nil
		}
		return nil, err
	}
	commits := []Commit{}
	for _, rec := range strings.Split(string(out), rs) {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, fs)
		if len(f) < 6 {
			continue
		}
		at, _ := strconv.ParseInt(f[4], 10, 64)
		c := Commit{Hash: f[0], Short: short(f[0]), Parents: strings.Fields(f[1]), Refs: parseRefs(f[2]), Author: f[3], Time: at, Subject: f[5]}
		if c.Parents == nil {
			c.Parents = []string{}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// parseRefs 解析 %D（--decorate=full）：“HEAD -> refs/heads/main, refs/remotes/origin/main, tag: refs/tags/v1”。
func parseRefs(s string) []Ref {
	var refs []Ref
	for _, part := range strings.Split(s, ", ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		head := false
		switch {
		case part == "HEAD":
			refs = append(refs, Ref{Name: "HEAD", Kind: "branch", Head: true})
			continue
		case strings.HasPrefix(part, "HEAD -> "):
			head = true
			part = strings.TrimPrefix(part, "HEAD -> ")
		}
		switch {
		case strings.HasPrefix(part, "tag: "):
			refs = append(refs, Ref{Name: strings.TrimPrefix(strings.TrimPrefix(part, "tag: "), "refs/tags/"), Kind: "tag"})
		case strings.HasPrefix(part, "refs/remotes/"):
			name := strings.TrimPrefix(part, "refs/remotes/")
			if strings.HasSuffix(name, "/HEAD") { // origin/HEAD 只是个别名
				continue
			}
			refs = append(refs, Ref{Name: name, Kind: "remote"})
		case strings.HasPrefix(part, "refs/heads/"):
			refs = append(refs, Ref{Name: strings.TrimPrefix(part, "refs/heads/"), Kind: "branch", Head: head})
		case strings.HasPrefix(part, "refs/"): // 其他引用（stash、notes 等）不显示
		default:
			refs = append(refs, Ref{Name: part, Kind: "branch", Head: head})
		}
	}
	return refs
}
