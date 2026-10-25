# 客户端自动更新

## 目标与取舍

- 用户在应用里看到"发现新版本"，点一下就能更新并自动重启。
- 不依赖 Wails V3（V3 仍是 beta；它的更新器也只替代"下载并替换"这一段，发布流水线和签名密钥管理仍要自己做），留在 Wails V2，更新逻辑自己实现，几乎全是 Go 标准库，将来迁移也不受影响。
- 更新机制本身是最大的攻击面（谁能篡改下载源，谁就能让所有客户端执行任意程序），所以**必须验签**。

## 整体流程

```mermaid
sequenceDiagram
    participant Dev as 发布者
    participant CI as GitHub Actions
    participant GH as GitHub Releases
    participant App as 客户端
    Dev->>CI: git push tag v1.2.3
    CI->>CI: 三平台打包（-ldflags 写入版本号）
    CI->>CI: cmd/mkmanifest 生成 manifest.json 并用私钥签名
    CI->>GH: 创建 Release（安装包 + manifest.json + manifest.json.sig）
    App->>GH: GET releases/latest/download/manifest.json(.sig)
    App->>App: 用内置公钥验签 → 比较版本
    App->>GH: 下载安装包 → 校验大小和 sha256
    App->>App: 替换可执行文件 → 启动新进程 → 退出
```

## 清单与签名

`manifest.json`：

```json
{
  "version": "1.2.3",
  "notes": "- 更新说明…",
  "pub_date": "2026-10-25T07:50:39Z",
  "page": "https://github.com/…/releases/tag/v1.2.3",
  "platforms": {
    "windows-amd64": {"url": "https://…/myllmapp-windows-amd64.exe", "sha256": "…", "size": 16285184},
    "linux-amd64":   {"url": "https://…/myllmapp-linux-amd64", "sha256": "…", "size": 15500144}
  }
}
```

`manifest.json.sig` 是对 `manifest.json` 全部字节的 ed25519 签名（base64）。客户端的检查顺序：下载清单和签名 → **先验签** → 再解析 JSON → 比较版本 → 校验平台条目。验签前的内容不进入任何解析逻辑。

两层校验各自防什么：签名保证清单是发布者发的（包括里面的下载地址和 sha256）；sha256 和大小保证下载到的安装包就是清单里说的那个。因此下载地址即使被劫持，也只能导致下载失败，不能让客户端装上别的程序。

其他约束：下载地址必须是 https（本机回环地址允许 http，仅供测试）；下载的字节数超过清单声明就中止；任何一步失败都不会动当前程序。

## 版本号

- 发布构建用 `-ldflags "-X github.com/crazyjums/myllmapp/internal/update.Version=1.2.3"` 写入；本地开发构建是 `dev`，**不检查更新**，免得开发时被提示更新并覆盖自己编译的程序。
- 比较规则（`update.Newer`）：按 主.次.修订 比较；同一个版本号下正式版比预发布版新；两个预发布版之间不比较。带连字符的 tag（`v1.2.3-beta.1`）发布时标成预发布，不会成为 `latest`，所以正式版用户收不到。

## 各平台怎么替换

| 平台 | 方式 |
| --- | --- |
| Windows | 运行中的 exe 不能覆盖，但可以改名：当前 exe → `.old`，新文件 → 原名；启动新进程后退出；新进程启动时清理 `.old`。第二步失败会把 `.old` 改回去（回滚）。 |
| Linux | 同样先改名再替换（Linux 也允许替换运行中的文件）。仅适用于直接运行的二进制；用 deb/rpm 安装的应走系统包管理器。 |
| macOS | **不自动安装**：`.app` 是目录，没有签名和公证时替换容易被 Gatekeeper 拦截。只提示新版本并打开发布页（`Info.Manual`）。 |

重启时新进程会带环境变量 `MYLLMAPP_UPDATED_FROM=<旧进程 pid>`，启动时先等旧进程退出（最多 10 秒）再初始化，避免两个实例同时打开同一个 SQLite 数据库。

程序目录不可写（例如装在 `C:\Program Files` 且没有管理员权限）时，下载这一步就会失败并提示手动安装；更新不会尝试提权。

## 代码位置

| 位置 | 作用 |
| --- | --- |
| `internal/update/` | 版本比较、清单验签、下载校验、替换、重启（不依赖 Wails，可直接 `go test`） |
| `internal/update/pubkey.txt` | 内置公钥（base64）。**为空时更新功能不可用**，界面上会说明原因 |
| `internal/app/update.go` | 绑定：`GetVersion`、`CheckUpdate`、`InstallUpdate`（后台下载）、`GetUpdateStatus`（前端轮询进度） |
| `cmd/mkmanifest/` | 发布侧工具：`genkey` 生成密钥，`sign` 生成并签名清单 |
| `.github/workflows/release.yml` | 打 tag 触发：三平台打包 → 签名清单 → 创建 Release |
| `frontend/src/store/updateStore.ts`、`UpdateBanner.tsx`、`AboutSettings.tsx` | 检查调度、横幅、设置 → 关于 |

前端只触发"检查"和"安装"；**安装时用的下载地址和 sha256 取自后端保存的最近一次检查结果**，不接受前端传来的任何地址。

## 一次性准备

1. 生成密钥：`go run ./cmd/mkmanifest genkey`。
2. 把**公钥**写进 `internal/update/pubkey.txt` 并提交。
3. 把**私钥**保存为 GitHub 仓库的加密变量 `UPDATE_SIGNING_KEY`（Settings → Secrets and variables → Actions）。**私钥绝不能提交进仓库，也不要发给任何人**；丢失或泄露时需要重新生成密钥，并发布一个用新公钥打包的版本——旧版本客户端只认旧公钥，所以泄露后旧用户只能手动重装一次。
4. 发布：`git tag v1.2.3 && git push origin v1.2.3`。流水线的第一步 `preflight` 会运行 `mkmanifest check-key`，确认私钥已配置、`pubkey.txt` 非空且与私钥是同一对；不通过就直接失败，不会发布一个没人能更新的版本。

### 流水线做了什么（`.github/workflows/release.yml`）

1. `preflight`：检查签名密钥（见上）。
2. `build`（Windows / Linux / macOS 三个平台并行）：跑 Go 和前端测试 → `wails build`（带 `-ldflags` 写入版本号）→ 上传产物。产物名：`myllmapp-windows-amd64.exe`、`myllmapp-linux-amd64`、`myllmapp-darwin-universal.zip`。
3. `release`：下载三个产物 → 用 git 提交记录生成更新说明 → `mkmanifest sign` 生成并签名 `manifest.json` / `manifest.json.sig` → `gh release create` 创建 Release，上传全部文件。带连字符的 tag 标为预发布。

仓库需要允许 Actions 写入：Settings → Actions → General → Workflow permissions 选 "Read and write permissions"（工作流里已声明 `contents: write`，组织仓库有时会被上层策略覆盖）。

本地验证（不经过 GitHub）：用 `cmd/mkmanifest sign` 生成清单，起一个本地静态服务器，运行时设置 `MYLLMAPP_UPDATE_URL=http://127.0.0.1:端口/manifest.json`，并用 `VERSION=1.0.0 ./scripts/build.sh` 构建一个旧版本。

## 局限与后续

- 没有 Windows / macOS 的代码签名：Windows 首次安装可能出现 SmartScreen 提示。这和更新机制本身无关，需要购买证书解决。
- 仓库必须公开（或把清单和安装包放在可匿名访问的地址）。私有仓库不能把访问令牌打包进客户端；可以用 `MYLLMAPP_UPDATE_URL` 指向自己的对象存储。
- 没有增量更新、回滚到旧版本的界面和灰度发布。
- 清单只有一个通道（最新正式版）。要做 beta 通道，可以让清单地址可配置并分别发布。
