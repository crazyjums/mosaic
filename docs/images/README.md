# README 截图

这四张 `readme-*.png` 截图来自 **实际 React 前端页面**，由浏览器演示模式生成；不是 AI 画的 UI 概念图，也不是连接真实模型的桌面端截图。

- `readme-chat.png`：fake Provider 的会话与执行视图
- `readme-workspace.png`：空白工作区与左右侧栏
- `readme-artifacts.png`：右侧产物标签
- `readme-plugins.png`：插件中心（演示数据）

## 如何重新截图

推荐在 Linux/macOS 开发环境执行：

```bash
cd frontend
npm ci
npm run dev -- --host 127.0.0.1
```

另开一个终端，在仓库根目录执行：

```bash
python -m pip install playwright
python -m playwright install chromium
python scripts/capture-readme-screenshots.py
```

脚本默认访问 `http://127.0.0.1:5173`，可用 `--url` 指定其他本地地址。截图前需确认运行的是浏览器演示模式（页面能看到“演示模式”），而不是接入真实 Go 后端的 Wails 页面。

注意：

1. 所有图片必须使用**假数据**；不要把用户路径、真实工作会话、模型 Key、私人文档、OAuth 地址或错误中的敏感信息提交到公开仓库。
2. 重新生成后请人工检查对话卡片、右侧上下文/产物切换、插件中心有没有遮挡、弹窗和 UI 错位；并核对 README 文案与当次正式 Release。
3. 当前截图是 1600 × 900 的浅色桌面布局；若布局有改动，应同步更新本脚本与 README 中的功能描述。
4. 仓库里的老截图用于历史对比，不再作为 README 的“当前界面”展示图。
