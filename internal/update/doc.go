// Package update 实现客户端的自更新：检查新版本 → 下载并校验 → 替换可执行文件 → 重启。
//
// # 发布侧产物（由 cmd/mkmanifest 在 CI 里生成，见 .github/workflows/release.yml）
//
//   - manifest.json：版本号、更新说明、各平台安装包的下载地址 / sha256 / 大小；
//   - manifest.json.sig：对 manifest.json 全部字节的 ed25519 签名（base64）。
//
// # 为什么要签名
//
// 更新机制本身就是最大的攻击面：谁能篡改下载源，谁就能让所有客户端执行任意程序。
// 所以客户端内置公钥（pubkey.txt），清单必须验签通过才信任；清单里的 sha256 又保证安装包与清单一致。
// 私钥只放在 CI 的加密变量里，不进仓库。这样即使 GitHub 账号之外的下载通道被劫持，也无法伪造更新。
//
// # 各平台的替换方式
//
//   - Windows：运行中的 exe 不能覆盖但可以改名，所以先把当前 exe 改名为 .old，再把新文件改名顶上，
//     然后启动新进程并退出；下次启动时清理 .old。
//   - Linux：新文件 chmod 后直接 rename 覆盖（Linux 允许替换运行中的文件）。
//   - macOS：.app 是目录且没有签名 / 公证时替换容易被 Gatekeeper 拦截，所以只提示新版本并打开下载页（Manual）。
//
// # 不负责什么
//
// 不负责界面（见 internal/app 的绑定和前端的更新提示），不依赖 Wails，所以可以用普通的 go test 测试。
package update
