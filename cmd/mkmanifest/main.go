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
	default:
		fail("未知命令 " + os.Args[1])
	}
}

func sign(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	version := fs.String("version", "", "版本号，例如 1.2.3")
	baseURL := fs.String("base-url", "", "安装包的下载目录地址（不含文件名）")
	notesFile := fs.String("notes-file", "", "更新说明文件（可选）")
	page := fs.String("page", "", "发布页地址（可选）")
	out := fs.String("out", ".", "输出目录")
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
