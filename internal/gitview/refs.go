package gitview

import (
	"context"
	"strconv"
	"strings"
)

// Branch 是一个分支或标签。
type Branch struct {
	Name string `json:"name"`
	// Kind：branch / remote / tag
	Kind string `json:"kind"`
	Hash string `json:"hash"`
	// Current 表示是当前检出的分支。
	Current bool `json:"current,omitempty"`
	// Upstream 是本地分支跟踪的远端分支；Ahead/Behind 是相对它的领先/落后提交数。
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
	Time     int64  `json:"time"`
	Subject  string `json:"subject,omitempty"`
}

const branchFormat = "%(refname)" + fs + "%(objectname)" + fs + "%(*objectname)" + fs + "%(HEAD)" + fs +
	"%(upstream:short)" + fs + "%(upstream:track,nobracket)" + fs + "%(creatordate:unix)" + fs + "%(contents:subject)" + rs

// Branches 列出本地分支、远端分支和标签。
func (r Repo) Branches(ctx context.Context) ([]Branch, error) {
	out, err := r.run(ctx, maxListBytes, "for-each-ref", "--format="+branchFormat, "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, err
	}
	list := []Branch{}
	for _, rec := range strings.Split(string(out), rs) {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, fs)
		if len(f) < 8 {
			continue
		}
		b := Branch{Hash: f[1], Current: f[3] == "*", Upstream: f[4], Subject: f[7]}
		b.Time, _ = strconv.ParseInt(f[6], 10, 64)
		if f[2] != "" { // 带注释的标签：指向的是 tag 对象，取它指向的提交
			b.Hash = f[2]
		}
		switch {
		case strings.HasPrefix(f[0], "refs/heads/"):
			b.Name, b.Kind = strings.TrimPrefix(f[0], "refs/heads/"), "branch"
			b.Ahead, b.Behind = parseTrack(f[5])
		case strings.HasPrefix(f[0], "refs/remotes/"):
			b.Name, b.Kind = strings.TrimPrefix(f[0], "refs/remotes/"), "remote"
			if strings.HasSuffix(b.Name, "/HEAD") {
				continue
			}
			b.Upstream = ""
		case strings.HasPrefix(f[0], "refs/tags/"):
			b.Name, b.Kind = strings.TrimPrefix(f[0], "refs/tags/"), "tag"
			b.Upstream = ""
		default:
			continue
		}
		list = append(list, b)
	}
	return list, nil
}

// parseTrack 解析 “ahead 2, behind 1” / “ahead 2” / “behind 1” / “gone”。
func parseTrack(s string) (ahead, behind int) {
	for _, part := range strings.Split(s, ",") {
		fields := strings.Fields(part)
		if len(fields) != 2 {
			continue
		}
		n, _ := strconv.Atoi(fields[1])
		switch fields[0] {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return
}
