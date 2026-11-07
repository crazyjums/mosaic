// 打包前的准备：构建前端和 mosaicd，连同 CHANGELOG.md 放进 desktop/stage/，供 electron-builder 当 extraResources。
//
// 用法：node scripts/stage.mjs [--version 1.8.0] [--universal]
//   --version    写进 mosaicd 的版本号（设置 → 关于、更新检查用）；不给就是 dev。
//   --universal  macOS 通用二进制：分别编译 arm64 和 amd64，再用 lipo 合并成一个。
import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const desktop = join(here, "..");
const repo = join(desktop, "..");
const stage = join(desktop, "stage");

const args = process.argv.slice(2);
const universal = args.includes("--universal");
const vi = args.indexOf("--version");
const version = vi >= 0 ? args[vi + 1] : "";

function run(cmd, argv, opts = {}) {
  const r = spawnSync(cmd, argv, { stdio: "inherit", shell: process.platform === "win32" && cmd === "npm", ...opts });
  if (r.status !== 0) {
    console.error(`失败：${cmd} ${argv.join(" ")}`);
    process.exit(r.status ?? 1);
  }
}

rmSync(stage, { recursive: true, force: true });
mkdirSync(stage, { recursive: true });

// 前端
if (!existsSync(join(repo, "frontend", "node_modules"))) run("npm", ["ci", "--no-audit", "--no-fund"], { cwd: join(repo, "frontend") });
run("npm", ["run", "build"], { cwd: join(repo, "frontend") });
cpSync(join(repo, "frontend", "dist"), join(stage, "frontend"), { recursive: true });
cpSync(join(repo, "CHANGELOG.md"), join(stage, "CHANGELOG.md"));
cpSync(join(repo, "build", "appicon.png"), join(stage, "icon.png")); // 托盘图标

// 后端：-trimpath 去掉构建机路径；版本号和 Wails 版共用同一个变量，更新检查逻辑不用改。
const exe = process.platform === "win32" ? "mosaicd.exe" : "mosaicd";
const ldflags = version ? `-X github.com/crazyjums/myllmapp/internal/update.Version=${version}` : "";
function build(out, goarch) {
  const argv = ["build", "-trimpath"];
  if (ldflags) argv.push("-ldflags", ldflags);
  argv.push("-o", out, "./cmd/mosaicd");
  run("go", argv, { cwd: repo, env: { ...process.env, CGO_ENABLED: "0", ...(goarch ? { GOARCH: goarch } : {}) } });
}
if (universal) {
  const a = join(stage, "mosaicd-arm64");
  const b = join(stage, "mosaicd-amd64");
  build(a, "arm64");
  build(b, "amd64");
  run("lipo", ["-create", "-output", join(stage, exe), a, b]);
  rmSync(a);
  rmSync(b);
} else {
  build(join(stage, exe));
}
console.log(`stage 完成：${stage}`);
