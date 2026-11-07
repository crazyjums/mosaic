package update

import (
	"fmt"
	"regexp"
	"strings"
)

// 合并到 master 后自动发布用的版本号与更新日志处理（mkmanifest bump 调用，见 .github/workflows/ci.yml）。
//
// 规则：
//   - 默认在最新的正式版 tag 上加 0.0.1（v1.7.9 → v1.7.10）；
//   - 如果 CHANGELOG.md 最上面已经手写了一个更大、还没打 tag 的版本（例如要升次版本号 1.8.0），就用它；
//   - PR 把面向用户的说明写在 [Unreleased] 下面；发布时这些内容自动变成新版本的小节；
//   - [Unreleased] 是空的（例如机器人提的 PR），就用合并进来的 PR 标题生成；连标题都分不出类（全是 docs / ci / chore），
//     就写一条“内部维护”，保证每个版本在应用的版本历史里都有内容。

// FallbackMaintenance 是没有任何面向用户的变更时写进新版本的那一条。
const FallbackMaintenance = "内部维护（文档、测试或构建流程的调整），功能没有变化。"

var unreleasedHeading = regexp.MustCompile(`(?i)^##\s+\[?unreleased\]?\s*$`)

// LatestStable 返回 tags 里最大的正式版（不带预发布后缀）；没有时返回 "0.0.0"。不认识的 tag 被忽略。
func LatestStable(tags []string) string {
	latest := "0.0.0"
	for _, t := range tags {
		t = strings.TrimSpace(t)
		_, pre, err := parseVersion(t)
		if t == "" || err != nil || pre {
			continue
		}
		if newer, _ := Newer(t, latest); newer {
			latest = strings.TrimPrefix(t, "v")
		}
	}
	return latest
}

// NextVersion 决定这次要发布的版本（不带 v）。topChangelog 是 CHANGELOG.md 里第一个正式版小节的版本号（可以为空）。
func NextVersion(tags []string, topChangelog string) (string, error) {
	latest := LatestStable(tags)
	if topChangelog != "" {
		if newer, err := Newer(topChangelog, latest); err == nil && newer && !hasTag(tags, topChangelog) {
			return strings.TrimPrefix(topChangelog, "v"), nil
		}
	}
	n, _, err := parseVersion(latest)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", n[0], n[1], n[2]+1), nil
}

func hasTag(tags []string, version string) bool {
	v := strings.TrimPrefix(version, "v")
	for _, t := range tags {
		if strings.TrimPrefix(strings.TrimSpace(t), "v") == v {
			return true
		}
	}
	return false
}

// TopVersion 返回 CHANGELOG 里第一个正式版小节的版本号；没有就返回空串。
func TopVersion(md string) string {
	for _, r := range ParseChangelog(md) {
		if _, pre, err := parseVersion(r.Version); err == nil && !pre {
			return r.Version
		}
	}
	return ""
}

// ApplyRelease 让 CHANGELOG 里有 version 的小节，返回新内容和是否改动了：
//   - 已经有这个小节：不改；
//   - [Unreleased] 下面有内容：在 [Unreleased] 标题之后插入 "## [version] - date"，原来的内容就归到新版本下，[Unreleased] 变空；
//   - [Unreleased] 是空的：插入新小节，内容是 fallback（为空时写一条“内部维护”）。
//
// 没有 [Unreleased] 标题时，新小节插在第一个版本小节之前（都没有就放在文件末尾）。
func ApplyRelease(md, version, date string, fallback Release) (string, bool, error) {
	version = strings.TrimPrefix(version, "v")
	if _, _, err := parseVersion(version); err != nil {
		return "", false, err
	}
	for _, r := range ParseChangelog(md) {
		if r.Version == version {
			return md, false, nil
		}
	}
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	heading := fmt.Sprintf("## [%s] - %s", version, date)

	unrel, next := -1, len(lines)
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if unrel < 0 && unreleasedHeading.MatchString(t) {
			unrel = i
			continue
		}
		if strings.HasPrefix(t, "## ") && (unrel >= 0 || releaseHeading.MatchString(t)) {
			next = i
			break
		}
	}

	if unrel >= 0 && hasContent(lines[unrel+1:next]) {
		out := append([]string{}, lines[:unrel+1]...)
		out = append(out, "", heading)
		out = append(out, lines[unrel+1:]...)
		return strings.Join(out, "\n"), true, nil
	}

	// 新小节插在下一个标题之前（有 [Unreleased] 时就紧跟在它后面），前后各空一行。
	block := append([]string{heading, ""}, renderSections(fallback)...)
	block = trimBlank(block)
	pre, post := trimBlank(lines[:next]), trimBlank(lines[next:])
	out := append(append([]string{}, pre...), "")
	out = append(out, block...)
	out = append(out, "")
	if len(post) > 0 {
		out = append(out, post...)
		out = append(out, "")
	}
	return strings.Join(out, "\n"), true, nil
}

// trimBlank 去掉首尾的空行。
func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func hasContent(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

// kindHeadings 是写回 CHANGELOG 时用的小节标题（中文，和手写的风格一致）。
var kindHeadings = map[string]string{
	KindBreaking: "不兼容变更", KindSecurity: "安全", KindAdded: "新增", KindChanged: "变更",
	KindFixed: "修复", KindDeprecated: "弃用", KindRemoved: "移除",
}

func renderSections(r Release) []string {
	secs := orderSections(cleanRelease(r).Sections)
	if len(secs) == 0 {
		secs = []Section{{Kind: KindChanged, Items: []string{FallbackMaintenance}}}
	}
	var out []string
	for _, s := range secs {
		out = append(out, "### "+kindHeadings[s.Kind])
		for _, it := range s.Items {
			out = append(out, "- "+it)
		}
		out = append(out, "")
	}
	return out
}

var mergePR = regexp.MustCompile(`^Merge pull request #(\d+) from `)

// SubjectsFromLog 把 `git log --first-parent --format=%s%x1f%b%x1e` 的输出转成更新日志兜底用的标题：
// 合并提交取 PR 标题（正文第一行）并带上 PR 编号；发布机器人自己的提交跳过。
func SubjectsFromLog(log string) []string {
	var out []string
	for _, rec := range strings.Split(log, "\x1e") {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		subject, body, _ := strings.Cut(rec, "\x1f")
		subject = strings.TrimSpace(subject)
		if strings.HasPrefix(subject, "chore(release)") {
			continue
		}
		if m := mergePR.FindStringSubmatch(subject); m != nil {
			title := ""
			for _, l := range strings.Split(body, "\n") {
				if t := strings.TrimSpace(l); t != "" {
					title = t
					break
				}
			}
			if title == "" {
				continue
			}
			subject = fmt.Sprintf("%s（#%s）", title, m[1])
		}
		if subject != "" {
			out = append(out, subject)
		}
	}
	return out
}
