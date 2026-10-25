package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version 是当前程序的版本号。发布构建时通过 -ldflags 注入：
//
//	-X github.com/crazyjums/myllmapp/internal/update.Version=1.2.3
//
// 本地开发构建保持 "dev"：开发版不检查更新，免得开发时被提示"有新版本"并覆盖掉自己编译的程序。
var Version = "dev"

// parseVersion 解析 "v1.2.3" / "1.2.3" / "1.2.3-beta.1"，返回主次修订号和是否带预发布后缀。
func parseVersion(s string) (nums [3]int, pre bool, err error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "+"); i >= 0 {
		s = s[:i] // 构建元数据不参与比较
	}
	if i := strings.Index(s, "-"); i >= 0 {
		s, pre = s[:i], true
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return nums, false, fmt.Errorf("版本号 %q 不是 主.次.修订 的格式", s)
	}
	for i, p := range parts {
		n, e := strconv.Atoi(p)
		if e != nil || n < 0 {
			return nums, false, fmt.Errorf("版本号 %q 不是 主.次.修订 的格式", s)
		}
		nums[i] = n
	}
	return nums, pre, nil
}

// Newer 判断 latest 是否比 current 新。同一个主次修订号下，正式版比预发布版新（1.0.0 > 1.0.0-beta.1）；
// 两个预发布版之间不比较先后（不会把用户从一个 beta 推到另一个 beta）。
func Newer(latest, current string) (bool, error) {
	l, lpre, err := parseVersion(latest)
	if err != nil {
		return false, err
	}
	c, cpre, err := parseVersion(current)
	if err != nil {
		return false, err
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i], nil
		}
	}
	return cpre && !lpre, nil
}
