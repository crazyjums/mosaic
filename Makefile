# 常用命令汇总。除 dev / build 外，所有目标都不需要 Wails CLI 和桌面系统依赖。

.PHONY: desktop-install desktop-test desktop-start desktop-smoke test vet check cli demo demo-events frontend-install frontend-check frontend-dev dev build package package-windows package-all

## Go：测试与静态检查
test:
	go test ./...

vet:
	go vet ./...

## 一次性跑完所有云端可验证的检查（Go + 前端）
check: vet test frontend-check

## CLI：交互模式（默认 fake 厂商的演示剧本，不需要密钥）
cli:
	go run ./cmd/cli

## CLI：非交互演示，自动回答两次权限确认（一次允许、一次拒绝）
demo:
	printf '帮我做个演示\ny\nn\n' | go run ./cmd/cli -demo -no-store -workspace $$(mktemp -d)

## 重新录制前端浏览器演示模式使用的事件流
demo-events:
	go run ./cmd/cli -demo -json -yes -no-store -workspace $$(mktemp -d) -p "帮我做个演示" 2>/dev/null > frontend/src/bridge/demo-events.jsonl

## 前端
frontend-install:
	cd frontend && npm install

frontend-check:
	cd frontend && npm run typecheck && npm test && npm run build

frontend-dev:
	cd frontend && npm run dev

## Electron 外壳（迁移中，见 docs/design/electron-migration.md）：需要 Node 22+；不需要 Wails
desktop-install:
	cd desktop && npm install

## 单元测试（含用真实的 mosaicd 验证协议）
desktop-test:
	cd desktop && npm test

## 在 Electron 窗口里启动（先构建前端；后端用 go run，也可以 MOSAIC_MOSAICD=路径 指定已编译的）
desktop-start:
	cd frontend && npm run build
	cd desktop && npm start

## 冒烟：真实窗口 + 离线演示厂商跑一轮对话（Linux 无显示器时自动用 xvfb-run）
desktop-smoke:
	cd frontend && npm run build
	cd desktop && npm run smoke

## 桌面端（需要本机安装 Wails CLI 与系统依赖，见 docs/usage.md）
dev:
	wails dev

build:
	wails build

## 一键打包（自动识别系统，见 scripts/build.sh -h）
package:
	./scripts/build.sh

## 打包 Windows 版本（macOS / Linux 上交叉编译）
package-windows:
	./scripts/build.sh --windows

## 本系统版本 + Windows 版本
package-all:
	./scripts/build.sh --all
