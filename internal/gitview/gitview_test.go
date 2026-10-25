package gitview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Ann", "GIT_AUTHOR_EMAIL=ann@example.com", "GIT_COMMITTER_NAME=Ann", "GIT_COMMITTER_EMAIL=ann@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo 建一个有分支、合并、标签、重命名的仓库：
//
//	c1 ── c2 ─────── m (main, HEAD) ← 合并 feature
//	       └─ f1 ──┘
func newRepo(t *testing.T) (Repo, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有安装 git")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	h := map[string]string{}
	write(t, dir, "a.txt", "one\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-q", "-m", "c1 初始提交")
	h["c1"] = mustGit(t, dir, "rev-parse", "HEAD")
	write(t, dir, "a.txt", "one\ntwo\n")
	mustGit(t, dir, "commit", "-q", "-am", "c2 第二个", "-m", "正文第一行")
	h["c2"] = mustGit(t, dir, "rev-parse", "HEAD")
	mustGit(t, dir, "tag", "-a", "v1.0", "-m", "release")
	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "b.txt", "feature\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-q", "-m", "f1 功能")
	h["f1"] = mustGit(t, dir, "rev-parse", "HEAD")
	mustGit(t, dir, "checkout", "-q", "main")
	mustGit(t, dir, "mv", "a.txt", "renamed.txt")
	mustGit(t, dir, "commit", "-q", "-m", "m0 重命名")
	h["m0"] = mustGit(t, dir, "rev-parse", "HEAD")
	mustGit(t, dir, "merge", "-q", "--no-ff", "feature", "-m", "m 合并 feature")
	h["m"] = mustGit(t, dir, "rev-parse", "HEAD")
	return Repo{Dir: dir}, h
}

func TestLogParentsAndRefs(t *testing.T) {
	r, h := newRepo(t)
	cs, err := r.Log(context.Background(), 0, 100)
	if err != nil || len(cs) != 5 {
		t.Fatalf("应有 5 个提交: %v %d", err, len(cs))
	}
	if cs[0].Hash != h["m"] || len(cs[0].Parents) != 2 {
		t.Fatalf("最新的是合并提交，有两个父提交: %+v", cs[0])
	}
	if cs[0].Subject != "m 合并 feature" || cs[0].Author != "Ann" || cs[0].Time == 0 {
		t.Fatalf("字段: %+v", cs[0])
	}
	hasRef := func(c Commit, name, kind string, head bool) bool {
		for _, rf := range c.Refs {
			if rf.Name == name && rf.Kind == kind && rf.Head == head {
				return true
			}
		}
		return false
	}
	if !hasRef(cs[0], "main", "branch", true) {
		t.Fatalf("HEAD -> main: %+v", cs[0].Refs)
	}
	byHash := map[string]Commit{}
	for _, c := range cs {
		byHash[c.Hash] = c
	}
	if !hasRef(byHash[h["f1"]], "feature", "branch", false) || !hasRef(byHash[h["c2"]], "v1.0", "tag", false) {
		t.Fatalf("feature 分支 / v1.0 标签: %+v %+v", byHash[h["f1"]].Refs, byHash[h["c2"]].Refs)
	}
	if byHash[h["c1"]].Parents == nil || len(byHash[h["c1"]].Parents) != 0 {
		t.Fatalf("根提交没有父提交（JSON 里应是 []）: %#v", byHash[h["c1"]].Parents)
	}
	// 分页
	page, err := r.Log(context.Background(), 2, 2)
	if err != nil || len(page) != 2 || page[0].Hash != cs[2].Hash {
		t.Fatalf("分页: %v %+v", err, page)
	}
}

func TestEmptyRepoAndNotRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有安装 git")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q", "-b", "main")
	r := Repo{Dir: dir}
	if cs, err := r.Log(context.Background(), 0, 10); err != nil || len(cs) != 0 {
		t.Fatalf("空仓库应返回空列表: %v %v", err, cs)
	}
	st, err := r.Status(context.Background())
	if err != nil || st.Branch != "main" || st.Head != "" {
		t.Fatalf("空仓库状态: %v %+v", err, st)
	}
	if _, err := (Repo{Dir: t.TempDir()}).Log(context.Background(), 0, 10); err != ErrNotRepo {
		t.Fatalf("非仓库应返回 ErrNotRepo: %v", err)
	}
}

func TestBranches(t *testing.T) {
	r, h := newRepo(t)
	// 加一个“远端”，让 main 有上游并落后/领先。
	remote := t.TempDir()
	mustGit(t, remote, "init", "-q", "--bare", "-b", "main")
	mustGit(t, r.Dir, "remote", "add", "origin", remote)
	mustGit(t, r.Dir, "push", "-q", "origin", "main")
	mustGit(t, r.Dir, "branch", "-q", "--set-upstream-to=origin/main", "main")
	write(t, r.Dir, "c.txt", "x\n")
	mustGit(t, r.Dir, "add", "-A")
	mustGit(t, r.Dir, "commit", "-q", "-m", "local only")

	bs, err := r.Branches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	find := func(name, kind string) *Branch {
		for i := range bs {
			if bs[i].Name == name && bs[i].Kind == kind {
				return &bs[i]
			}
		}
		return nil
	}
	main := find("main", "branch")
	if main == nil || !main.Current || main.Upstream != "origin/main" || main.Ahead != 1 || main.Behind != 0 {
		t.Fatalf("main: %+v", main)
	}
	if f := find("feature", "branch"); f == nil || f.Current || f.Hash != h["f1"] {
		t.Fatalf("feature: %+v", f)
	}
	if o := find("origin/main", "remote"); o == nil || o.Upstream != "" {
		t.Fatalf("origin/main: %+v", o)
	}
	if tg := find("v1.0", "tag"); tg == nil || tg.Hash != h["c2"] {
		t.Fatalf("带注释标签应指向提交而不是 tag 对象: %+v", tg)
	}
}

func TestStatusAndWorkingDiff(t *testing.T) {
	r, _ := newRepo(t)
	write(t, r.Dir, "renamed.txt", "one\ntwo\nthree\n") // 工作区修改
	write(t, r.Dir, "b.txt", "feature\nmore\n")         // 暂存
	mustGit(t, r.Dir, "add", "b.txt")
	write(t, r.Dir, "new 文件.txt", "hi\n")         // 未跟踪
	mustGit(t, r.Dir, "mv", "b.txt", "moved.txt") // 暂存区重命名

	st, err := r.Status(context.Background())
	if err != nil || st.Branch != "main" || st.Head == "" || st.Detached {
		t.Fatalf("status: %v %+v", err, st)
	}
	byPath := map[string]FileChange{}
	for _, f := range st.Files {
		byPath[f.Path] = f
	}
	if f := byPath["renamed.txt"]; f.Work != "M" || f.Staged() {
		t.Fatalf("renamed.txt: %+v", f)
	}
	if f := byPath["new 文件.txt"]; f.Index != "?" || f.Staged() {
		t.Fatalf("未跟踪（含中文和空格的文件名）: %+v", f)
	}
	if f := byPath["moved.txt"]; f.Index != "R" || f.OrigPath != "b.txt" || !f.Staged() {
		t.Fatalf("暂存区重命名: %+v", f)
	}

	d, err := r.WorkingDiff(context.Background(), "renamed.txt", false)
	if err != nil || !strings.Contains(d.Text, "+three") {
		t.Fatalf("工作区 diff: %v %+v", err, d)
	}
	d, err = r.WorkingDiff(context.Background(), "new 文件.txt", false)
	if err != nil || d.Text != "" || d.Note == "" {
		t.Fatalf("未跟踪文件给说明: %v %+v", err, d)
	}
	if _, err := r.WorkingDiff(context.Background(), "../../etc/passwd", false); err == nil {
		t.Fatal("不在变更列表里的路径应被拒绝")
	}
}

func TestCommitDetailAndDiff(t *testing.T) {
	r, h := newRepo(t)
	ctx := context.Background()

	d, err := r.Commit(ctx, h["c2"][:10]) // 短提交号
	if err != nil {
		t.Fatal(err)
	}
	if d.Hash != h["c2"] || d.Subject != "c2 第二个" || d.Body != "正文第一行" || d.Email != "ann@example.com" {
		t.Fatalf("提交信息: %+v", d)
	}
	if len(d.Files) != 1 || d.Files[0].Path != "a.txt" || d.Files[0].Status != "M" || d.Files[0].Added != 1 || d.Added != 1 {
		t.Fatalf("文件列表: %+v", d.Files)
	}
	diff, err := r.CommitDiff(ctx, h["c2"], "a.txt")
	if err != nil || !strings.Contains(diff.Text, "+two") {
		t.Fatalf("diff: %v %+v", err, diff)
	}

	// 根提交
	root, err := r.Commit(ctx, h["c1"])
	if err != nil || len(root.Parents) != 0 || len(root.Files) != 1 || root.Files[0].Status != "A" {
		t.Fatalf("根提交: %v %+v", err, root)
	}
	if diff, err = r.CommitDiff(ctx, h["c1"], "a.txt"); err != nil || !strings.Contains(diff.Text, "+one") {
		t.Fatalf("根提交 diff: %v %+v", err, diff)
	}

	// 重命名
	mv, err := r.Commit(ctx, h["m0"])
	if err != nil || len(mv.Files) != 1 || mv.Files[0].Status != "R" || mv.Files[0].OrigPath != "a.txt" || mv.Files[0].Path != "renamed.txt" {
		t.Fatalf("重命名: %v %+v", err, mv.Files)
	}

	// 合并提交：相对第一个父提交（main 一侧），带来 feature 的 b.txt
	merge, err := r.Commit(ctx, h["m"])
	if err != nil || len(merge.Parents) != 2 || len(merge.Files) != 1 || merge.Files[0].Path != "b.txt" {
		t.Fatalf("合并提交: %v %+v", err, merge.Files)
	}

	if _, err := r.CommitDiff(ctx, h["c2"], "b.txt"); err == nil {
		t.Fatal("该提交没改的文件应被拒绝")
	}
}

func TestInputValidation(t *testing.T) {
	r, h := newRepo(t)
	ctx := context.Background()
	for _, bad := range []string{"", "--output=/tmp/x", "-p", "HEAD", "main", "abc", h["c1"] + "; rm -rf /", "zzzzzzzz"} {
		if _, err := r.Commit(ctx, bad); err == nil {
			t.Errorf("提交号 %q 应被拒绝", bad)
		}
	}
	if _, err := r.Commit(ctx, "deadbeefdeadbeef"); err == nil {
		t.Error("不存在的提交应报错")
	}
}

// 不受信任的仓库可以在 .git/config 里指定要执行的程序；只读查看不能触发它们。
func TestRepoConfigCannotRunPrograms(t *testing.T) {
	r, _ := newRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	script := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, r.Dir, "config", "core.fsmonitor", script)
	mustGit(t, r.Dir, "config", "diff.external", script)
	write(t, r.Dir, "renamed.txt", "changed\n")
	ctx := context.Background()
	if _, err := r.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WorkingDiff(ctx, "renamed.txt", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("仓库配置里的外部程序被执行了")
	}
}

func TestDiffTruncation(t *testing.T) {
	r, _ := newRepo(t)
	write(t, r.Dir, "renamed.txt", strings.Repeat("0123456789abcdef\n", MaxDiffBytes/10))
	d, err := r.WorkingDiff(context.Background(), "renamed.txt", false)
	if err != nil || !d.Truncated || len(d.Text) > MaxDiffBytes {
		t.Fatalf("应截断: %v truncated=%v len=%d", err, d.Truncated, len(d.Text))
	}
}

func TestParseRefs(t *testing.T) {
	got := parseRefs("HEAD -> refs/heads/main, refs/remotes/origin/main, refs/remotes/origin/HEAD, tag: refs/tags/v1, refs/stash")
	want := []Ref{{Name: "main", Kind: "branch", Head: true}, {Name: "origin/main", Kind: "remote"}, {Name: "v1", Kind: "tag"}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v != %+v", i, got[i], want[i])
		}
	}
	if d := parseRefs("HEAD"); len(d) != 1 || !d[0].Head {
		t.Fatalf("detached HEAD: %+v", d)
	}
}
