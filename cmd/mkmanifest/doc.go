// Command mkmanifest 是发布侧的小工具：生成更新签名密钥、生成并签名更新清单（manifest.json）。
//
// 用法：
//
//	UPDATE_SIGNING_KEY=<私钥> mkmanifest check-key
//	    发布前自检：私钥存在，internal/update/pubkey.txt 非空，且两者是同一对密钥（流水线第一步运行）。
//
//	mkmanifest genkey
//	    生成一对 ed25519 密钥。公钥写进 internal/update/pubkey.txt 并提交；
//	    私钥保存为 GitHub 仓库的加密变量 UPDATE_SIGNING_KEY，绝不能提交进仓库。
//
//	UPDATE_SIGNING_KEY=<私钥> mkmanifest sign -version 1.2.3 -base-url https://github.com/o/r/releases/download/v1.2.3 \
//	    -notes-file notes.md -page https://github.com/o/r/releases/tag/v1.2.3 -out dist \
//	    -asset windows-amd64=dist/myllmapp-windows-amd64.exe -asset linux-amd64=dist/myllmapp-linux-amd64
//
//	    在 -out 目录写出 manifest.json 和 manifest.json.sig。-asset 的键是 <GOOS>-<GOARCH>，
//	    下载地址 = base-url + "/" + 文件名。macOS 不能自动更新，不需要 -asset，清单里只放 -page。
package main
