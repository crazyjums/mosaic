package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crazyjums/myllmapp/internal/update"
)

type assetFlags []string

func (a *assetFlags) String() string     { return strings.Join(*a, ",") }
func (a *assetFlags) Set(v string) error { *a = append(*a, v); return nil }

func main() {
	if len(os.Args) < 2 {
		fail("用法: mkmanifest genkey | sign ...（见文件头注释）")
	}
	switch os.Args[1] {
	case "genkey":
		pub, priv, err := update.GenerateKey()
		check(err)
		fmt.Println("公钥（写入 internal/update/pubkey.txt 并提交）:")
		fmt.Println(pub)
		fmt.Println()
		fmt.Println("私钥（保存为 GitHub 加密变量 UPDATE_SIGNING_KEY，绝不要提交进仓库）:")
		fmt.Println(priv)
	case "sign":
		sign(os.Args[2:])
	case "check-key":
		checkKey(os.Args[2:])
	default:
		fail("未知命令 " + os.Args[1])
	}
}

func sign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	version := fs.String("version", "", "版本号，例如 1.2.3")
	baseURL := fs.String("base-url", "", "安装包的下载目录地址（不含文件名）")
	notesFile := fs.String("notes-file", "", "纯文本更新说明（可选，覆盖自动生成的 notes；结构化日志不受影响）")
	changelog := fs.String("changelog", "", "CHANGELOG.md（Keep a Changelog 格式）；有对应版本的小节就用它")
	commitsFile := fs.String("commits-file", "", "提交标题列表（每行一条），CHANGELOG 里没有这个版本时按 Conventional Commits 前缀分类作为兜底")
	page := fs.String("page", "", "发布页地址（可选）")
	out := fs.String("out", ".", "输出目录")
	notesOut := fs.String("notes-out", "", "把本版本的更新日志写成 markdown 到这个文件（用作 GitHub Release 的正文）")
	var assets assetFlags
	fs.Var(&assets, "asset", "<GOOS>-<GOARCH>=<文件路径>，可重复")
	check(fs.Parse(args))
	if *version == "" || *baseURL == "" {
		fail("需要 -version 和 -base-url")
	}
	key := os.Getenv("UPDATE_SIGNING_KEY")
	if key == "" {
		fail("环境变量 UPDATE_SIGNING_KEY 未设置")
	}
	m := update.Manifest{Version: strings.TrimPrefix(*version, "v"), Page: *page, PubDate: time.Now().UTC().Format(time.RFC3339), Platforms: map[string]update.Asset{}}
	m.Releases = buildReleases(m.Version, m.PubDate[:10], *changelog, *commitsFile)
	if len(m.Releases) > 0 {
		// 旧版本客户端只认 notes：放最新一版的纯文本。
		m.Notes = update.Flatten(m.Releases[0], kindLabel)
	}
	if *notesFile != "" {
		b, err := os.ReadFile(*notesFile)
		check(err)
		m.Notes = strings.TrimSpace(string(b))
	}
	for _, a := range assets {
		platform, path, ok := strings.Cut(a, "=")
		if !ok {
			fail("-asset 格式应为 <GOOS>-<GOARCH>=<路径>: " + a)
		}
		size, sum := digest(path)
		m.Platforms[platform] = update.Asset{URL: strings.TrimRight(*baseURL, "/") + "/" + filepath.Base(path), SHA256: sum, Size: size}
	}
	body, err := json.MarshalIndent(m, "", "  ")
	check(err)
	sig, err := update.Sign(key, body)
	check(err)
	if *notesOut != "" && len(m.Releases) > 0 {
		check(os.WriteFile(*notesOut, []byte(markdown(m.Releases[0])), 0o644))
	}
	check(os.MkdirAll(*out, 0o755))
	check(os.WriteFile(filepath.Join(*out, "manifest.json"), body, 0o644))
	check(os.WriteFile(filepath.Join(*out, "manifest.json.sig"), sig, 0o644))
	fmt.Printf("已写出 %s 和 .sig（%d 个平台）\n", filepath.Join(*out, "manifest.json"), len(m.Platforms))
}

func digest(path string) (int64, string) {
	f, err := os.Open(path)
	check(err)
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	check(err)
	return n, hex.EncodeToString(h.Sum(nil))
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "错误:", msg)
	os.Exit(1)
}

// checkKey 发布前自检：私钥存在，internal/update/pubkey.txt 非空，并且两者是同一对密钥。
// 少了这一步，公钥没配或配错时流水线会照常发布，但所有客户端都无法验签，用户永远更新不了。
func checkKey(args []string) {
	fs := flag.NewFlagSet("check-key", flag.ExitOnError)
	pubFile := fs.String("pubkey", "internal/update/pubkey.txt", "内置公钥文件")
	check(fs.Parse(args))
	key := os.Getenv("UPDATE_SIGNING_KEY")
	if key == "" {
		fail("加密变量 UPDATE_SIGNING_KEY 没有设置（仓库 Settings → Secrets and variables → Actions）")
	}
	b, err := os.ReadFile(*pubFile)
	check(err)
	pub, err := update.ParsePublicKey(string(b))
	if err != nil {
		fail(*pubFile + ": " + err.Error() + "（先运行 mkmanifest genkey，把公钥写进该文件并提交）")
	}
	probe := []byte("myllmapp-key-check")
	sig, err := update.Sign(key, probe)
	check(err)
	if err := update.Verify(pub, probe, sig); err != nil {
		fail("私钥与 " + *pubFile + " 里的公钥不是一对，客户端将无法验证更新")
	}
	fmt.Println("签名密钥检查通过")
}

// maxReleases 是清单里保留的版本数：用户可能隔了好几个版本才更新，要能看到中间的全部变更；
// 但清单每次检查都会下载，不能无限增长。
const maxReleases = 10

var kindLabels = map[string]string{
	update.KindBreaking: "不兼容变更", update.KindSecurity: "安全", update.KindAdded: "新增", update.KindChanged: "变更",
	update.KindFixed: "修复", update.KindDeprecated: "弃用", update.KindRemoved: "移除",
}

func kindLabel(k string) string { return kindLabels[k] }

// buildReleases 组装清单里的结构化更新日志：最新的一版在最前，最多 maxReleases 个。
// 本版本在 CHANGELOG.md 里有小节就用手写的；没有就用提交标题兜底，并用 GitHub Actions 的警告提醒补写。
func buildReleases(version, date, changelogPath, commitsPath string) []update.Release {
	var all []update.Release
	if changelogPath != "" {
		b, err := os.ReadFile(changelogPath)
		check(err)
		all = update.ParseChangelog(string(b))
	}
	var out []update.Release
	for _, r := range all {
		if above, err := update.Newer(r.Version, version); err == nil && !above {
			out = append(out, r)
		}
	}
	if len(out) == 0 || out[0].Version != version {
		fmt.Fprintf(os.Stderr, "::warning::CHANGELOG.md 里没有 %s 的小节，已用提交标题自动生成更新日志；建议补写后再发布\n", version)
		var subjects []string
		if commitsPath != "" {
			b, err := os.ReadFile(commitsPath)
			check(err)
			subjects = strings.Split(string(b), "\n")
		}
		if r := update.FromCommits(subjects, version, date); len(r.Sections) > 0 {
			out = append([]update.Release{r}, out...)
		}
	}
	if len(out) > maxReleases {
		out = out[:maxReleases]
	}
	return out
}

// markdown 把一个版本的日志渲染成 GitHub Release 的正文。
func markdown(r update.Release) string {
	var b strings.Builder
	if r.Summary != "" {
		b.WriteString(r.Summary + "\n\n")
	}
	for _, sec := range r.Sections {
		b.WriteString("### " + kindLabel(sec.Kind) + "\n")
		for _, it := range sec.Items {
			b.WriteString("- " + it + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}
