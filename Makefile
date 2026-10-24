# 常用命令汇总。除 dev / build 外，所有目标都不需要 Wails CLI 和桌面系统依赖。

.PHONY: test vet check cli demo demo-events frontend-install frontend-check frontend-dev dev build package package-windows package-all

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
	printf '帮我做个演示\ny\nn\n' | go run ./cmd/cli -no-store -workspace $$(mktemp -d)

## 重新录制前端浏览器演示模式使用的事件流
demo-events:
	go run ./cmd/cli -json -yes -no-store -workspace $$(mktemp -d) -p "帮我做个演示" 2>/dev/null > frontend/src/bridge/demo-events.jsonl

## 前端
frontend-install:
	cd frontend && npm install

frontend-check:
	cd frontend && npm run typecheck && npm test && npm run build

frontend-dev:
	cd frontend && npm run dev

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
