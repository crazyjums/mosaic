package gitview

import (
	"context"
	"errors"
	"strings"
)

// FileChange 是工作区里一个有变化的文件。
type FileChange struct {
	Path string `json:"path"`
	// OrigPath 只在重命名/复制时有值（原路径）。
	OrigPath string `json:"orig_path,omitempty"`
	// Index / Work 是 git status --porcelain 的两列状态字母：暂存区、工作区。
	// 常见值：M 修改、A 新增、D 删除、R 重命名、? 未跟踪、U 冲突，空格表示无变化。
	Index string `json:"index"`
	Work  string `json:"work"`
}

// Staged 表示暂存区里有这个文件的改动。
func (f FileChange) Staged() bool { return f.Index != " " && f.Index != "?" && f.Index != "" }

// Status 是工作区状态。
type Status struct {
	// Branch 是当前分支；detached HEAD 时为空，Detached 为 true。
	Branch   string       `json:"branch"`
	Detached bool         `json:"detached"`
	Head     string       `json:"head"` // HEAD 的完整提交号，空仓库为空
	Upstream string       `json:"upstream,omitempty"`
	Ahead    int          `json:"ahead,omitempty"`
	Behind   int          `json:"behind,omitempty"`
	Files    []FileChange `json:"files"`
}

// Status 返回当前分支、与上游的差距和有变化的文件。
func (r Repo) Status(ctx context.Context) (Status, error) {
	out, err := r.run(ctx, maxListBytes, "status", "--porcelain=v1", "-z", "-b")
	if err != nil {
		return Status{}, err
	}
	st := Status{Files: []FileChange{}}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "## ") {
			parseBranchLine(&st, e[3:])
			continue
		}
		if len(e) < 4 {
			continue
		}
		fc := FileChange{Index: e[0:1], Work: e[1:2], Path: e[3:]}
		// 重命名/复制：下一个条目是原路径
		if fc.Index == "R" || fc.Index == "C" || fc.Work == "R" || fc.Work == "C" {
			if i+1 < len(entries) {
				fc.OrigPath = entries[i+1]
				i++
			}
		}
		st.Files = append(st.Files, fc)
	}
	if h, err := r.run(ctx, 1024, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		st.Head = strings.TrimSpace(string(h))
	}
	return st, nil
}

// parseBranchLine 解析 “main...origin/main [ahead 1, behind 2]”、“No commits yet on main”、“HEAD (no branch)”。
func parseBranchLine(st *Status, s string) {
	if rest, ok := strings.CutPrefix(s, "No commits yet on "); ok {
		st.Branch = rest
		return
	}
	if strings.HasPrefix(s, "HEAD (no branch)") {
		st.Detached = true
		return
	}
	track := ""
	if i := strings.Index(s, " ["); i >= 0 && strings.HasSuffix(s, "]") {
		track = s[i+2 : len(s)-1]
		s = s[:i]
	}
	name, up, _ := strings.Cut(s, "...")
	st.Branch, st.Upstream = name, up
	st.Ahead, st.Behind = parseTrack(track)
}

// Diff 是一个文件的 diff 文本。
type Diff struct {
	Path      string `json:"path"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
	// Binary 表示是二进制文件，没有文本 diff。
	Binary bool `json:"binary,omitempty"`
	// Note 是补充说明（例如“未跟踪的文件，还没有 diff”）。
	Note string `json:"note,omitempty"`
}

// WorkingDiff 返回工作区里某个文件的 diff。staged=true 看暂存区（相对 HEAD），否则看工作区（相对暂存区）。
// path 必须是 Status 里出现的文件，避免被当作任意路径读取。
func (r Repo) WorkingDiff(ctx context.Context, path string, staged bool) (Diff, error) {
	st, err := r.Status(ctx)
	if err != nil {
		return Diff{}, err
	}
	var fc *FileChange
	for i := range st.Files {
		if st.Files[i].Path == path {
			fc = &st.Files[i]
			break
		}
	}
	if fc == nil {
		return Diff{}, errors.New("这个文件现在没有改动")
	}
	if fc.Index == "?" {
		return Diff{Path: path, Note: "未跟踪的文件：还没有加入 git，所以没有 diff。"}, nil
	}
	args := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	return r.diff(ctx, path, args)
}

func (r Repo) diff(ctx context.Context, path string, args []string) (Diff, error) {
	var out []byte
	var err error
	// 多读 1 字节用来判断是否被截断
	out, err = r.run(ctx, MaxDiffBytes+1, args...)
	if err != nil {
		return Diff{}, err
	}
	d := Diff{Path: path}
	if len(out) > MaxDiffBytes {
		out, d.Truncated = out[:MaxDiffBytes], true
	}
	d.Text = strings.ToValidUTF8(string(out), "�")
	if strings.Contains(d.Text, "\nBinary files ") || strings.HasPrefix(d.Text, "Binary files ") || strings.Contains(d.Text, "\nGIT binary patch") {
		d.Binary = true
	}
	return d, nil
}
