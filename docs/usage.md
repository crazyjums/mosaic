# 使用文档

## 1. 环境要求

| 用途 | 需要 |
| --- | --- |
| 内核、CLI、测试 | Go ≥ 1.26（本机更旧时，默认的 `GOTOOLCHAIN=auto` 会自动下载） |
| 前端 | Node.js ≥ 20.19 或 ≥ 22.12（Vite 8 的要求），npm |
| 桌面端 | 以上全部 + Wails CLI v2.17 + 平台依赖（见下） |

### 安装 Wails CLI 与平台依赖

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
wails doctor   # 检查依赖，缺什么会列出来
```

| 平台 | 依赖 |
| --- | --- |
| macOS | Xcode 命令行工具：`xcode-select --install` |
| Windows | WebView2 运行时（Windows 10/11 通常已自带） |
| Linux (Debian/Ubuntu 22.04) | `sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev` |
| Linux (Ubuntu 24.04+ / Debian 13+) | `sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev`，并在 wails 命令后加 `-tags webkit2_41` |

## 2. 配置厂商

不写任何配置也能运行：内置默认配置包含 fake（离线演示）以及 anthropic / openai / deepseek 三个配置档（从环境变量读密钥）。

需要自定义时：

```bash
cp config.example.toml config.toml      # 或 ~/.myllmapp/config.toml
export ANTHROPIC_API_KEY=...            # 密钥放在环境变量里
export DEEPSEEK_API_KEY=...
```

配置文件查找顺序：命令行 `-config` > 环境变量 `MYLLMAPP_CONFIG` > `./config.toml` > `~/.myllmapp/config.toml` > 内置默认。

每个字段的说明见 [`config.example.toml`](../config.example.toml)。要点：

- `[[providers]]` 的 `type` 是适配器（`openai` / `anthropic` / `fake`），`name` 是你起的名字。所有 OpenAI 兼容厂商都用 `type = "openai"` + 各自的 `base_url`。
- 密钥：`api_key_env` 指向环境变量（推荐）；`api_key` 直接写在文件里（不推荐）。两者都有时环境变量优先。
- **不要提交 `config.toml`**（已在 `.gitignore` 中）。仓库里有 `internal/archtest` 测试会扫描疑似密钥。
- 缺少密钥的配置档不会导致启动失败，只是在界面上显示为不可用并给出原因。

数据（会话数据库）默认在 `~/.myllmapp/sessions.db`，可用 `data_dir` 修改。

## 3. 用 CLI 调试

CLI 和桌面端使用完全相同的内核，是调试 agent 行为最快的方式。

```bash
# 交互模式（默认 fake 演示剧本，不需要密钥）
go run ./cmd/cli

# 一次性演示：第一行是问题，后面两行回答两次权限确认（允许写文件、拒绝执行命令）
make demo
# 等价于
printf '帮我做个演示\ny\nn\n' | go run ./cmd/cli -no-store -workspace "$(mktemp -d)"

# 使用真实厂商
go run ./cmd/cli -provider anthropic -model claude-opus-5-5
go run ./cmd/cli -provider deepseek -p "列出当前目录下的 Go 文件"

# 导出原始事件流（JSONL），排查协议问题最直接的办法
go run ./cmd/cli -json -yes -p "hi" > events.jsonl
```

| 参数 | 说明 |
| --- | --- |
| `-config` | 配置文件路径 |
| `-provider` / `-model` | 覆盖默认厂商配置档和模型 |
| `-workspace` | 工作区目录（工具读写的根目录） |
| `-mode` | 权限模式：`default` / `auto` / `readonly` |
| `-p "..."` | 单次执行后退出 |
| `-yes` | 自动允许所有权限请求（谨慎） |
| `-json` | 以 JSONL 输出原始事件，提示信息走 stderr |
| `-no-store` | 不写数据库 |
| `-resume <id>` | 恢复会话 |
| `-sessions` | 列出已保存的会话后退出 |

交互模式命令：`/help`、`/new`、`/sessions`、`/load <id>`、`/models`、`/model <profile> [model]`、`/status`、`/quit`。
**Ctrl-C** 取消当前这一轮（空闲时退出），**Ctrl-D** 退出。权限确认时输入 `y` 允许一次、`a` 本会话总是允许、`n` 拒绝。

## 4. 前端单独开发（浏览器演示模式）

```bash
cd frontend
npm install
npm run dev        # http://localhost:5173
```

浏览器里没有 Go 后端，前端会自动切换到**演示模式**（左上角有标记），回放 fake provider 录制的事件流，权限弹窗可以真实点击。适合调界面样式。

修改了 fake 剧本或事件协议后，重新录制演示数据：

```bash
make demo-events
```

前端检查：

```bash
npm run typecheck   # 类型检查
npm test            # Vitest（reducer 单元测试）
npm run build       # 生产构建
```

## 5. 桌面端开发模式

```bash
wails dev                    # Linux 新发行版：wails dev -tags webkit2_41
```

Wails 会启动 Vite 开发服务器并打开桌面窗口，前端热更新，Go 代码改动后自动重新编译。
开发模式下也可以用浏览器访问 Wails 打印出的地址（通常是 http://localhost:34115），此时调用的是真实 Go 后端。

> 根目录 `main.go` 带有构建标签 `desktop || dev || production || bindings`，Wails CLI 会自动传入，平时的 `go build ./...` / `go test ./...` 会跳过它。

## 6. 打包

```bash
wails build                  # 产物在 build/bin/
wails build -platform darwin/universal
wails build -platform windows/amd64 -nsis   # Windows 安装包（需要 NSIS）
```

首次 `wails build` 会在 `build/` 下生成图标、Info.plist、Windows manifest 等平台资源，可以按需修改后提交。

## 7. 一键检查

```bash
make check     # go vet + go test + 前端类型检查/测试/构建
```

## 8. 常见问题

| 现象 | 原因 / 处理 |
| --- | --- |
| `go: downloading go1.26...` | 本机 Go 低于 1.26，自动下载工具链，正常现象 |
| `pattern all:frontend/dist: no matching files found` | 直接 `go build -tags desktop` 时前端还没构建。用 `wails build`，或先 `cd frontend && npm run build` |
| Linux 上找不到 `webkit2gtk-4.0` | 新发行版只有 4.1，加 `-tags webkit2_41` |
| 界面上厂商显示"不可用：missing API key" | 对应环境变量没有设置，或桌面应用没有继承终端的环境变量（macOS 从 Dock 启动时常见）——可以在 `config.toml` 中用 `api_key` 直接配置，或从终端启动 |
| Anthropic 报 400 与 thinking 相关 | 旧模型或代理不支持 adaptive thinking：配置该配置档 `options.thinking = "omit"` |
| OpenAI 报 `max_tokens` 不支持 | 设置 `options.max_tokens_field = "max_completion_tokens"` |
