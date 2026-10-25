# Mosaic 界面设计系统

本文记录 Mosaic 的品牌、设计 Tokens 和 Agent 状态动画的规则，改界面前先读一遍。

## 1. 品牌

- **Logo**：3×3 圆角拼块，八块海军蓝（三个深浅），中心一块金色。金色拼块代表"正在执行的任务"。组件是 `components/MosaicMark.tsx`，纯 CSS（`index.css` 的 `.mos-*`），没有渐变和阴影，小尺寸也清晰。
- **名称**：用户看到的名字统一是 **Mosaic**。底层标识（Go 模块名、`~/.myllmapp` 数据目录、`.myllmapp/context.md`、发布产物 `myllmapp-<平台>`、自动更新的资产名）保持 `myllmapp` 不变，改了会让老用户升级后找不到数据。
- 品牌色是常量，**不随皮肤变化**：`--gold`、`--mos-1/2/3`。

## 2. Tokens 分三层

| 层 | 内容 | 谁能改 |
| --- | --- | --- |
| 调色板（12 个） | `--bg --panel --panel-2 --border --fg --muted --accent --on-accent --think --ok --warn --danger` | 皮肤（`store/skins.ts`），用户可自定义 |
| 派生 | `--side`（侧栏 / 状态栏底色）、`--accent-soft`（选中底色）、`--accent-hover`、`--code-bg`、`--brand`（主按钮）、`--focus-ring` | 不能改；由调色板用 `color-mix()` 算出，换皮肤自动协调 |
| 品牌与形状 | `--gold` `--mos-*`、`--radius-ctl/card/modal`（8 / 14 / 18 px）、`--shadow-card/pop`、`--sidebar-width`、`--modal-width`、`--content-width` | 设计系统 |

Tailwind 工具类：`bg-side`、`bg-accent-soft`、`bg-code`、`bg-brand text-on-brand`、`text-gold-ink`、`rounded-ctl|card|modal`；公共类：`.card`、`.btn`、`.btn-primary`、`.input`、`.shadow-card`、`.shadow-pop`。

**新增皮肤只需要提供第一层的 12 个颜色**（每个皮肤深色、浅色各一份）。`skins.test.ts` 会检查每个内置皮肤的对比度（正文 ≥ 7、次要文字 ≥ 4.5）。默认皮肤的颜色必须和 `index.css` 一致（测试检查），因为选中默认皮肤时不注入任何规则。

## 3. Agent 状态动画

Logo 的金块和执行状态卡片根据真实的 Agent 阶段变化（`store/agentPhase.ts`，纯函数、有单测）：

| 阶段 | 触发（真实事件） | 动画 |
| --- | --- | --- |
| `start` | 运行中，模型还没有任何输出 | 拼块依次出现 |
| `thinking` | 运行中，已有思考 / 工具调用 / 文字 | 金块沿九宫格缓慢移动，其余拼块轻微呼吸 |
| `problem` | 有权限请求在等你；或一轮以错误结束 | 金块停在右侧并变淡 |
| `done` | 一轮正常结束 | 金块回到中心，轻轻一弹，约 3 秒后静止 |
| `static` | 其他 | 静止 |

唯一的计时器只用来让"完成 / 出错"的反馈播放完后回到静止（`store/useAgentPhase.ts`），不决定任何执行状态。系统开启"减少动态效果"时所有拼块动画停止。博士动画（`ScholarThinking`）保留，和 Mosaic 拼块并排显示。

## 4. 版式规则

- 用户的提问和模型的最终回答优先；思考和工具调用折叠成一行摘要（`思考与执行 · N 步 · M 次工具调用 · 耗时`），失败数用红色标出。正文不包厚重的卡片。
- 失败的工具调用：卡片红色描边，不展开也显示错误的第一行。
- 液态玻璃 / 半透明只允许用在浮层；正文、代码块、终端输出必须是实色底和足够的对比度。
