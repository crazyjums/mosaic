package gitview

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// ChangedFile 是一个提交里变化的文件。
type ChangedFile struct {
	Path string `json:"path"`
	// OrigPath 只在重命名/复制时有值。
	OrigPath string `json:"orig_path,omitempty"`
	// Status：A 新增 / M 修改 / D 删除 / R 重命名 / C 复制 / T 类型变化
	Status string `json:"status"`
	// Added/Deleted 是增删行数；二进制文件为 -1。
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

// CommitDetail 是单个提交的详细信息。
type CommitDetail struct {
	Commit
	Email   string        `json:"email"`
	Body    string        `json:"body"`
	Files   []ChangedFile `json:"files"`
	Added   int           `json:"added"`
	Deleted int           `json:"deleted"`
}

const detailFormat = "%H" + fs + "%P" + fs + "%D" + fs + "%an" + fs + "%at" + fs + "%s" + fs + "%ae" + fs + "%b" + rs

// resolve 校验并规范化提交号：只接受十六进制，并确认它确实是一个提交。
func (r Repo) resolve(ctx context.Context, hash string) (string, error) {
	if !hashRe.MatchString(hash) {
		return "", errors.New("无效的提交号")
	}
	out, err := r.run(ctx, 256, "rev-parse", "--verify", "--quiet", hash+"^{commit}")
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", errors.New("找不到这个提交")
	}
	return strings.TrimSpace(string(out)), nil
}

// Commit 返回一个提交的信息和变化的文件（merge 提交相对第一个父提交）。
func (r Repo) Commit(ctx context.Context, hash string) (CommitDetail, error) {
	full, err := r.resolve(ctx, hash)
	if err != nil {
		return CommitDetail{}, err
	}
	out, err := r.run(ctx, 1<<20, "show", "-s", "--decorate=full", "--format="+detailFormat, full)
	if err != nil {
		return CommitDetail{}, err
	}
	f := strings.Split(strings.TrimRight(string(out), "\n"+rs), fs)
	if len(f) < 8 {
		return CommitDetail{}, errors.New("无法解析提交信息")
	}
	at, _ := strconv.ParseInt(f[4], 10, 64)
	d := CommitDetail{
		Commit: Commit{Hash: f[0], Short: short(f[0]), Parents: strings.Fields(f[1]), Refs: parseRefs(f[2]), Author: f[3], Time: at, Subject: f[5]},
		Email:  f[6],
		Body:   strings.TrimSpace(f[7]),
	}
	if d.Parents == nil {
		d.Parents = []string{}
	}

	// 变化的文件：与第一个父提交比较；根提交用 --root。
	base := []string{"diff-tree", "-r", "-M", "-z", "--no-commit-id", "--no-ext-diff", "--no-textconv"}
	var target []string
	if len(d.Parents) > 0 {
		target = []string{d.Parents[0], full}
	} else {
		base = append(base, "--root")
		target = []string{full}
	}
	statusOut, err := r.run(ctx, maxListBytes, append(append(append([]string{}, base...), "--name-status"), target...)...)
	if err != nil {
		return CommitDetail{}, err
	}
	numOut, err := r.run(ctx, maxListBytes, append(append(append([]string{}, base...), "--numstat"), target...)...)
	if err != nil {
		return CommitDetail{}, err
	}
	d.Files = parseNameStatus(string(statusOut))
	nums := parseNumstat(string(numOut))
	for i := range d.Files {
		if n, ok := nums[d.Files[i].Path]; ok {
			d.Files[i].Added, d.Files[i].Deleted = n[0], n[1]
			if n[0] > 0 {
				d.Added += n[0]
			}
			if n[1] > 0 {
				d.Deleted += n[1]
			}
		}
	}
	return d, nil
}

// parseNameStatus 解析 diff-tree --name-status -z：“M\0path\0” 或 “R100\0old\0new\0”。
func parseNameStatus(s string) []ChangedFile {
	files := []ChangedFile{}
	parts := strings.Split(s, "\x00")
	for i := 0; i < len(parts); i++ {
		st := parts[i]
		if st == "" {
			continue
		}
		cf := ChangedFile{Status: st[:1]}
		if cf.Status == "R" || cf.Status == "C" {
			if i+2 >= len(parts) {
				break
			}
			cf.OrigPath, cf.Path = parts[i+1], parts[i+2]
			i += 2
		} else {
			if i+1 >= len(parts) {
				break
			}
			cf.Path = parts[i+1]
			i++
		}
		files = append(files, cf)
	}
	return files
}

// parseNumstat 解析 diff-tree --numstat -z：“1\t2\tpath\0” 或重命名时 “1\t2\t\0old\0new\0”。键是新路径。
func parseNumstat(s string) map[string][2]int {
	m := map[string][2]int{}
	parts := strings.Split(s, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if p == "" {
			continue
		}
		f := strings.SplitN(p, "\t", 3)
		if len(f) < 3 {
			continue
		}
		a, errA := strconv.Atoi(f[0])
		d, errD := strconv.Atoi(f[1])
		if errA != nil || errD != nil { // 二进制文件显示为 “-”
			a, d = -1, -1
		}
		path := f[2]
		if path == "" && i+2 < len(parts) { // 重命名
			path = parts[i+2]
			i += 2
		}
		m[path] = [2]int{a, d}
	}
	return m
}

// CommitDiff 返回一个提交里某个文件的 diff（相对第一个父提交）。
// path 必须是该提交变化的文件之一。
func (r Repo) CommitDiff(ctx context.Context, hash, path string) (Diff, error) {
	d, err := r.Commit(ctx, hash)
	if err != nil {
		return Diff{}, err
	}
	found := false
	for _, f := range d.Files {
		if f.Path == path {
			found = true
			break
		}
	}
	if !found {
		return Diff{}, errors.New("这个提交没有改动该文件")
	}
	args := []string{"diff-tree", "-p", "-M", "--no-color", "--no-ext-diff", "--no-textconv", "--no-commit-id"}
	if len(d.Parents) > 0 {
		args = append(args, d.Parents[0], d.Hash)
	} else {
		args = append(args, "--root", d.Hash)
	}
	args = append(args, "--", path)
	return r.diff(ctx, path, args)
}
