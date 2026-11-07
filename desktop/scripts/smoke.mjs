#!/usr/bin/env node
// 在 Electron 里跑冒烟测试（见 src/smoke.ts）。Linux 没有显示器时自动套上 xvfb-run。
// 前提：已经构建了前端（cd frontend && npm run build）和 desktop（npm run build）。
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";

const electron = createRequire(import.meta.url)("electron");
const args = [".", "--smoke", ...(process.platform === "linux" ? ["--no-sandbox"] : [])];
const [cmd, argv] = process.platform === "linux" && !process.env.DISPLAY ? ["xvfb-run", ["-a", electron, ...args]] : [electron, args];
const r = spawnSync(cmd, argv, { stdio: "inherit", cwd: new URL("..", import.meta.url).pathname });
process.exit(r.status ?? 1);
