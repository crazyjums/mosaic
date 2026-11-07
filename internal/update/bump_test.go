package update

import (
	"strings"
	"testing"
)

func TestNextVersion(t *testing.T) {
	tags := []string{"v1.7.8", "v1.7.9", "v1.8.0-beta.1", "junk", ""}
	cases := []struct {
		tags []string
		top  string
		want string
	}{
		{tags, "1.7.9", "1.7.10"}, // 最上面的小节已经发过：加 0.0.1
		{tags, "", "1.7.10"},      // CHANGELOG 里没有版本小节
		{tags, "1.8.0", "1.8.0"},  // 手写了更大的、还没打 tag 的版本：用它（升次版本号的办法）
		{tags, "1.7.5", "1.7.10"}, // 比最新 tag 还小：忽略
		{append(tags, "v1.8.0"), "1.8.0", "1.8.1"},
		{nil, "", "0.0.1"},
	}
	for _, c := range cases {
		got, err := NextVersion(c.tags, c.top)
		if err != nil || got != c.want {
			t.Errorf("NextVersion(%v, %q) = %q, %v; want %q", c.tags, c.top, got, err, c.want)
		}
	}
	if LatestStable(tags) != "1.7.9" {
		t.Fatalf("预发布版不算最新正式版: %s", LatestStable(tags))
	}
}

const head = "# 更新日志\n\n说明文字。\n\n"

func TestApplyReleaseMovesUnreleased(t *testing.T) {
	md := head + "## [Unreleased]\n\n### 修复\n- 修好了一个问题\n\n## [1.7.9] - 2026-11-01\n\n### 新增\n- 旧功能\n"
	got, changed, err := ApplyRelease(md, "v1.7.10", "2026-11-07", Release{})
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	want := head + "## [Unreleased]\n\n## [1.7.10] - 2026-11-07\n\n### 修复\n- 修好了一个问题\n\n## [1.7.9] - 2026-11-01\n\n### 新增\n- 旧功能\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	rs := ParseChangelog(got)
	if rs[0].Version != "1.7.10" || rs[0].Sections[0].Items[0] != "修好了一个问题" || rs[1].Version != "1.7.9" {
		t.Fatalf("%+v", rs)
	}
	if TopVersion(got) != "1.7.10" {
		t.Fatal(TopVersion(got))
	}
}

func TestApplyReleaseFallback(t *testing.T) {
	md := head + "## [Unreleased]\n\n## [1.7.9] - 2026-11-01\n\n### 新增\n- 旧功能\n"
	fb := FromCommits([]string{"[ImgBot] Optimize images（#49）", "ci: 自动发布（#50）"}, "1.7.10", "2026-11-07")
	got, changed, err := ApplyRelease(md, "1.7.10", "2026-11-07", fb)
	if err != nil || !changed {
		t.Fatal(err)
	}
	want := head + "## [Unreleased]\n\n## [1.7.10] - 2026-11-07\n\n### 变更\n- [ImgBot] Optimize images（#49）\n\n## [1.7.9] - 2026-11-01\n\n### 新增\n- 旧功能\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}

	// 全是 docs / ci 这类不面向用户的提交：写一条“内部维护”，保证版本历史里不是空的（发布预检也要求有小节）。
	got, _, _ = ApplyRelease(md, "1.7.10", "2026-11-07", FromCommits([]string{"docs: x", "ci: y"}, "1.7.10", ""))
	rs := ParseChangelog(got)
	if rs[0].Version != "1.7.10" || len(rs[0].Sections) != 1 || rs[0].Sections[0].Items[0] != FallbackMaintenance {
		t.Fatalf("%+v", rs[0])
	}
}

func TestApplyReleaseEdgeCases(t *testing.T) {
	md := head + "## [Unreleased]\n\n## [1.8.0] - 2026-11-07\n\n### 新增\n- 手写的\n"
	if got, changed, _ := ApplyRelease(md, "1.8.0", "2026-11-08", Release{}); changed || got != md {
		t.Fatal("已经有这个版本的小节时不应改动")
	}
	// 没有 [Unreleased]：插在第一个版本之前。
	md = head + "## [1.7.9] - 2026-11-01\n\n### 新增\n- 旧功能\n"
	got, _, _ := ApplyRelease(md, "1.7.10", "2026-11-07", Release{})
	if !strings.HasPrefix(got, head+"## [1.7.10] - 2026-11-07\n\n### 变更\n- "+FallbackMaintenance+"\n\n## [1.7.9]") {
		t.Fatalf("%q", got)
	}
	// 文件里一个小节都没有。
	got, _, _ = ApplyRelease("# 更新日志\n", "0.0.1", "2026-11-07", Release{})
	if TopVersion(got) != "0.0.1" {
		t.Fatalf("%q", got)
	}
	// 署名行和会话链接不能写进公开的更新日志。
	fb := FromCommits([]string{"fix: 修了 https://claude.ai/code/session_x"}, "1.7.10", "")
	got, _, _ = ApplyRelease(head+"## [Unreleased]\n", "1.7.10", "2026-11-07", fb)
	if strings.Contains(got, "claude.ai") {
		t.Fatalf("%q", got)
	}
	if _, _, err := ApplyRelease(md, "abc", "", Release{}); err == nil {
		t.Fatal("非法版本号应报错")
	}
}

func TestSubjectsFromLog(t *testing.T) {
	log := "Merge pull request #49 from crazyjums/imgbot\x1f[ImgBot] Optimize images\n\nbody\x1e\n" +
		"chore(release): v1.7.10 [skip release]\x1f\x1e\n" +
		"fix: 直接提交到 master 的修复\x1f\x1e\n" +
		"Merge pull request #50 from x/y\x1f\n\x1e"
	got := SubjectsFromLog(log)
	if len(got) != 2 || got[0] != "[ImgBot] Optimize images（#49）" || got[1] != "fix: 直接提交到 master 的修复" {
		t.Fatalf("%q", got)
	}
}
