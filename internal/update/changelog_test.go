package update

import (
	"context"
	"reflect"
	"testing"
)

const sampleChangelog = `# 更新日志

本项目遵循 Keep a Changelog。

## [Unreleased]

### 新增
- 还没发布的东西

## [1.2.0] - 2026-10-25

支持自动更新，并修复了几个崩溃。

### Added
- 更新横幅
- 设置 → 关于
  显示版本号

### 修复
- 修复启动时偶尔崩溃

### Security
- 修复令牌写入日志的问题

## 1.1.0 - 2026-09-01
### 变更
- 调整默认步数

### Fixed
- 老问题

## [1.0.0-beta.1]
### 不兼容变更
- 配置文件改名
`

func TestParseChangelog(t *testing.T) {
	rs := ParseChangelog(sampleChangelog)
	if len(rs) != 3 {
		t.Fatalf("应跳过 Unreleased，得到 %d 个版本: %+v", len(rs), rs)
	}
	r := rs[0]
	if r.Version != "1.2.0" || r.Date != "2026-10-25" || r.Summary != "支持自动更新，并修复了几个崩溃。" {
		t.Fatalf("头部: %+v", r)
	}
	// 展示顺序：安全 → 新增 → 修复（不是文件里的写法顺序）
	var kinds []string
	for _, s := range r.Sections {
		kinds = append(kinds, s.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{KindSecurity, KindAdded, KindFixed}) {
		t.Fatalf("分类和顺序: %v", kinds)
	}
	if got := r.Sections[1].Items; !reflect.DeepEqual(got, []string{"更新横幅", "设置 → 关于 显示版本号"}) {
		t.Fatalf("缩进续行应并入上一条: %v", got)
	}
	if rs[1].Version != "1.1.0" || rs[1].Date != "2026-09-01" {
		t.Fatalf("不带方括号的标题: %+v", rs[1])
	}
	if rs[2].Version != "1.0.0-beta.1" || rs[2].Sections[0].Kind != KindBreaking {
		t.Fatalf("预发布版本和不兼容变更: %+v", rs[2])
	}
}

func TestAdvise(t *testing.T) {
	mk := func(kinds ...string) []Release {
		var secs []Section
		for _, k := range kinds {
			secs = append(secs, Section{Kind: k, Items: []string{"x"}})
		}
		return []Release{{Version: "1.0.0", Sections: secs}}
	}
	cases := []struct {
		name     string
		in       []Release
		level    string
		breaking bool
	}{
		{"只有新功能", mk(KindAdded, KindChanged), LevelOptional, false},
		{"有修复", mk(KindAdded, KindFixed), LevelRecommended, false},
		{"有安全修复", mk(KindFixed, KindSecurity), LevelCritical, false},
		{"不兼容变更", mk(KindAdded, KindBreaking), LevelRecommended, true},
		{"安全 + 不兼容", mk(KindSecurity, KindBreaking), LevelCritical, true},
		{"空", nil, LevelOptional, false},
		{"空小节不算", []Release{{Sections: []Section{{Kind: KindSecurity}}}}, LevelOptional, false},
	}
	for _, c := range cases {
		level, breaking := Advise(c.in)
		if level != c.level || breaking != c.breaking {
			t.Errorf("%s: 得到 %s/%v，期望 %s/%v", c.name, level, breaking, c.level, c.breaking)
		}
	}
	// 跳过了多个版本：只要其中任何一个有安全修复，整体就是 critical。
	two := []Release{{Version: "1.2.0", Sections: []Section{{Kind: KindAdded, Items: []string{"a"}}}}, {Version: "1.1.0", Sections: []Section{{Kind: KindSecurity, Items: []string{"s"}}}}}
	if l, _ := Advise(two); l != LevelCritical {
		t.Fatalf("跨版本应取最高等级: %s", l)
	}
}

func TestFromCommits(t *testing.T) {
	r := FromCommits([]string{
		"feat: 新增自动更新",
		"fix(core): 修复崩溃",
		"docs: 补文档",
		"Merge pull request #4 from x/y",
		"update: 开发版提示",
		"feat!: 配置文件改名",
		"没有前缀的提交",
		"ci: 调流水线",
	}, "v1.3.0", "2026-10-25")
	got := map[string][]string{}
	for _, s := range r.Sections {
		got[s.Kind] = s.Items
	}
	want := map[string][]string{
		KindBreaking: {"配置文件改名"},
		KindAdded:    {"新增自动更新"},
		KindChanged:  {"开发版提示", "没有前缀的提交"},
		KindFixed:    {"修复崩溃"},
	}
	if r.Version != "1.3.0" || !reflect.DeepEqual(got, want) {
		t.Fatalf("得到 %+v", got)
	}
	if r.Sections[0].Kind != KindBreaking {
		t.Fatal("不兼容变更应排在最前")
	}
}

func TestNewerReleases(t *testing.T) {
	all := []Release{{Version: "1.0.0"}, {Version: "1.3.0"}, {Version: "1.1.0"}, {Version: "1.2.0"}, {Version: "2.0.0"}}
	var got []string
	for _, r := range newerReleases(all, "1.0.0", "1.3.0") {
		got = append(got, r.Version)
	}
	if !reflect.DeepEqual(got, []string{"1.3.0", "1.2.0", "1.1.0"}) {
		t.Fatalf("应只含当前之后、不超过最新的，最新在前: %v", got)
	}
}

func TestCheckCarriesReleases(t *testing.T) {
	f := newFixture(t, "1.3.0")
	f.manifest.Releases = []Release{
		{Version: "1.3.0", Sections: []Section{{Kind: KindAdded, Items: []string{"新功能"}}}},
		{Version: "1.2.1", Sections: []Section{{Kind: KindSecurity, Items: []string{"安全修复"}}}},
		{Version: "1.2.0", Sections: []Section{{Kind: KindFixed, Items: []string{"旧的"}}}},
	}
	info, err := f.updater(t, "1.2.0", "linux").Check(context.Background())
	if err != nil || len(info.Releases) != 2 || info.Level != LevelCritical {
		t.Fatalf("应带上 1.2.0 之后的两个版本且为 critical: %+v %v", info, err)
	}
	// 旧格式清单（没有 releases）：不给建议等级，Notes 仍在。
	f.manifest.Releases = nil
	info, _ = f.updater(t, "1.2.0", "linux").Check(context.Background())
	if info.Level != "" || info.Notes == "" {
		t.Fatalf("旧格式: %+v", info)
	}
}

// 已经是最新版本时，Check 要把当前版本自己的日志带出来，关于页才有东西可显示。
func TestCheckCarriesCurrentRelease(t *testing.T) {
	f := newFixture(t, "1.3.0")
	f.manifest.Releases = []Release{
		{Version: "1.3.0", Sections: []Section{{Kind: KindAdded, Items: []string{"新功能"}}}},
		{Version: "1.2.0", Sections: []Section{{Kind: KindFixed, Items: []string{"旧的"}}}},
	}
	info, err := f.updater(t, "1.3.0", "linux").Check(context.Background())
	if err != nil || info.Available || info.CurrentRelease == nil || info.CurrentRelease.Version != "1.3.0" {
		t.Fatalf("应带上 1.3.0 自己的日志: %+v %v", info, err)
	}
	// 清单里没有当前版本（比如本地开发版）：不带，也不报错。
	info, err = f.updater(t, "1.4.0", "linux").Check(context.Background())
	if err != nil || info.CurrentRelease != nil {
		t.Fatalf("找不到应为空: %+v %v", info, err)
	}
}
