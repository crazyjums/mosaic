package update

import (
	"regexp"
	"sort"
	"strings"
)

// 更新日志的数据模型和解析。
//
// 遵循 Keep a Changelog（keepachangelog.com）：每个版本一节 "## [1.2.3] - 2026-10-25"，
// 下面按类型分组：新增 / 变更 / 弃用 / 移除 / 修复 / 安全。在此基础上增加一类"不兼容变更"（breaking），
// 对应 Conventional Commits 的 BREAKING CHANGE，需要用户在更新前知道。
//
// 为什么在清单里放结构化数据而不是一大段文本：客户端要据此回答"值不值得更新"——
// 有安全修复就提示尽快更新，只有新功能就是可选更新；纯文本做不到，也没法分类展示。

// 变更类型。值是写进清单的稳定标识，界面再翻译成文字。
const (
	KindBreaking   = "breaking"
	KindSecurity   = "security"
	KindAdded      = "added"
	KindChanged    = "changed"
	KindFixed      = "fixed"
	KindDeprecated = "deprecated"
	KindRemoved    = "removed"
)

// kindOrder 是展示顺序：用户最需要先知道的在前。
var kindOrder = []string{KindBreaking, KindSecurity, KindAdded, KindChanged, KindFixed, KindDeprecated, KindRemoved}

// Section 是一个版本里同一类型的若干条变更。
type Section struct {
	Kind  string   `json:"kind"`
	Items []string `json:"items"`
}

// Release 是一个版本的更新日志。
type Release struct {
	Version string `json:"version"`
	Date    string `json:"date,omitempty"`
	// Summary 是一句话摘要（版本标题下面的第一段），用于在列表里快速判断。
	Summary  string    `json:"summary,omitempty"`
	Sections []Section `json:"sections"`
}

// 更新建议等级，由 Advise 根据变更内容得出。
const (
	LevelCritical    = "critical"    // 含安全修复：建议尽快更新
	LevelRecommended = "recommended" // 含问题修复或不兼容变更：建议更新
	LevelOptional    = "optional"    // 只有新功能和改进：可选
)

// Advise 根据一批版本（用户当前版本之后的全部更新）给出更新建议等级，以及是否含不兼容变更。
func Advise(rs []Release) (level string, breaking bool) {
	level = LevelOptional
	for _, r := range rs {
		for _, s := range r.Sections {
			if len(s.Items) == 0 {
				continue
			}
			switch s.Kind {
			case KindSecurity:
				return LevelCritical, breaking || hasKind(rs, KindBreaking)
			case KindBreaking:
				breaking = true
				level = LevelRecommended
			case KindFixed, KindRemoved:
				level = LevelRecommended
			}
		}
	}
	return level, breaking
}

func hasKind(rs []Release, kind string) bool {
	for _, r := range rs {
		for _, s := range r.Sections {
			if s.Kind == kind && len(s.Items) > 0 {
				return true
			}
		}
	}
	return false
}

// Flatten 把一个版本的日志拼成纯文本，写进清单的 notes 字段，给不认识 releases 的旧版本客户端显示。
func Flatten(r Release, label func(kind string) string) string {
	var b strings.Builder
	if r.Summary != "" {
		b.WriteString(r.Summary + "\n\n")
	}
	for _, s := range r.Sections {
		if len(s.Items) == 0 {
			continue
		}
		b.WriteString(label(s.Kind) + "\n")
		for _, it := range s.Items {
			b.WriteString("- " + it + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// headingKinds 把 Keep a Changelog 的标准标题（英文）和常用中文写法映射到变更类型。
var headingKinds = map[string]string{
	"added": KindAdded, "新增": KindAdded, "新功能": KindAdded,
	"changed": KindChanged, "变更": KindChanged, "改进": KindChanged, "优化": KindChanged,
	"deprecated": KindDeprecated, "弃用": KindDeprecated,
	"removed": KindRemoved, "移除": KindRemoved, "删除": KindRemoved,
	"fixed": KindFixed, "修复": KindFixed,
	"security": KindSecurity, "安全": KindSecurity,
	"breaking": KindBreaking, "breaking changes": KindBreaking, "不兼容变更": KindBreaking, "重大变更": KindBreaking,
}

var releaseHeading = regexp.MustCompile(`^##\s+\[?v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.\-]+)?)\]?(?:\s*[-–—]\s*(\d{4}-\d{2}-\d{2}))?`)

// ParseChangelog 解析 Keep a Changelog 格式的 markdown，返回各版本的日志（保持文件里的顺序，通常最新的在前）。
// [Unreleased] 和不认识的标题会被忽略。
func ParseChangelog(md string) []Release {
	var out []Release
	var cur *Release
	var sec *Section
	var summary []string
	inRelease := false

	flush := func() {
		if cur == nil {
			return
		}
		cur.Summary = strings.TrimSpace(strings.Join(summary, " "))
		cur.Sections = orderSections(cur.Sections)
		out = append(out, *cur)
		cur, sec, summary = nil, nil, nil
	}

	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "## "):
			flush()
			inRelease = false
			if m := releaseHeading.FindStringSubmatch(trim); m != nil {
				cur = &Release{Version: m[1], Date: m[2]}
				inRelease = true
			}
		case !inRelease:
			// 文件头部的说明、[Unreleased] 等
		case strings.HasPrefix(trim, "### "):
			name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trim, "### ")))
			if kind, ok := headingKinds[name]; ok {
				cur.Sections = append(cur.Sections, Section{Kind: kind})
				sec = &cur.Sections[len(cur.Sections)-1]
			} else {
				sec = nil
			}
		case strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* "):
			if sec != nil {
				sec.Items = append(sec.Items, strings.TrimSpace(trim[2:]))
			}
		case trim == "":
		case sec != nil && len(sec.Items) > 0 && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")):
			// 缩进的续行并入上一条
			sec.Items[len(sec.Items)-1] += " " + trim
		case sec == nil && len(cur.Sections) == 0 && !strings.HasPrefix(trim, "#"):
			summary = append(summary, trim)
		}
	}
	flush()
	return out
}

// 同一类型出现多次（作者写了两个"修复"小节）时合并，并按 kindOrder 排序，丢弃空的。
func orderSections(in []Section) []Section {
	by := map[string][]string{}
	for _, s := range in {
		by[s.Kind] = append(by[s.Kind], s.Items...)
	}
	var out []Section
	for _, k := range kindOrder {
		if len(by[k]) > 0 {
			out = append(out, Section{Kind: k, Items: by[k]})
		}
	}
	return out
}

var conventional = regexp.MustCompile(`^(\w+)(?:\(([^)]*)\))?(!)?[:：]\s*(.+)$`)

// 提交前缀 → 变更类型。空串表示这类提交不面向用户，不写进更新日志。
var commitKinds = map[string]string{
	"feat": KindAdded, "add": KindAdded, "新增": KindAdded,
	"fix": KindFixed, "修复": KindFixed, "hotfix": KindFixed,
	"sec": KindSecurity, "security": KindSecurity,
	"perf": KindChanged, "refactor": KindChanged, "update": KindChanged, "change": KindChanged,
	"tool": KindChanged, "core": KindChanged, "frontend": KindChanged, "provider": KindChanged,
	"docs": "", "doc": "", "test": "", "tests": "", "ci": "", "chore": "", "build": "", "style": "", "release": "", "merge": "",
}

// FromCommits 在没有手写更新日志时的兜底：按 Conventional Commits 的前缀把提交标题分类。
// feat → 新增，fix → 修复，带 "!" 的（feat!: …）→ 不兼容变更；docs / test / ci / chore 等不写入。
// 不认识的前缀归入"变更"。它只是兜底：自动生成的日志远不如人写的好读，CI 里会提示补写 CHANGELOG.md。
func FromCommits(subjects []string, version, date string) Release {
	by := map[string][]string{}
	for _, s := range subjects {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "Merge ") {
			continue
		}
		kind, text := KindChanged, s
		if m := conventional.FindStringSubmatch(s); m != nil {
			prefix := strings.ToLower(m[1])
			text = m[4]
			if k, known := commitKinds[prefix]; known {
				if k == "" {
					continue
				}
				kind = k
			}
			if m[3] == "!" {
				kind = KindBreaking
			}
		}
		by[kind] = append(by[kind], text)
	}
	r := Release{Version: strings.TrimPrefix(version, "v"), Date: date}
	for _, k := range kindOrder {
		if len(by[k]) > 0 {
			r.Sections = append(r.Sections, Section{Kind: k, Items: by[k]})
		}
	}
	return r
}

// newerReleases 返回比 current 新、且不超过 latest 的版本，最新的在前。
func newerReleases(all []Release, current, latest string) []Release {
	var out []Release
	for _, r := range all {
		newer, err := Newer(r.Version, current)
		if err != nil || !newer {
			continue
		}
		if above, err := Newer(r.Version, latest); err != nil || above {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		n, _ := Newer(out[i].Version, out[j].Version)
		return n
	})
	return out
}
