import { t } from "../i18n";

// Mosaic 的 Logo：3×3 圆角拼块，八块海军蓝，中间一块金色。
//
// 金色拼块代表"正在执行的任务"：Logo 同时是 Agent 的状态指示——
//   static   静止，金块在中心（默认 Logo）
//   start    开始：拼块依次出现
//   thinking 思考 / 执行中：金块沿九宫格缓慢移动，其余拼块轻微呼吸
//   problem  需要你处理（等待确认、出错）：金块停在右侧并变淡，提示"这里卡住了"
//   done     完成：金块回到中心，轻轻一弹后静止
// 状态由真实的 Agent 事件决定（见 store/agentPhase.ts），不是定时器假装的进度。
// 样式在 index.css 的 .mos-*；系统开启"减少动态效果"时全部静止。

export type MosaicState = "static" | "start" | "thinking" | "problem" | "done";
export type MosaicSize = "xs" | "sm" | "md" | "lg";

const LABEL: Record<MosaicState, string> = {
  static: "Mosaic",
  start: "Mosaic · 开始思考",
  thinking: "Mosaic · 正在工作",
  problem: "Mosaic · 需要你处理",
  done: "Mosaic · 已完成",
};

export default function MosaicMark({ size = "md", state = "static", className = "", decorative = true }: { size?: MosaicSize; state?: MosaicState; className?: string; decorative?: boolean }) {
  return (
    <span
      className={`mos-grid mos-${size} ${className}`}
      data-state={state}
      role={decorative ? undefined : "img"}
      aria-hidden={decorative ? true : undefined}
      aria-label={decorative ? undefined : t(LABEL[state])}
    >
      {Array.from({ length: 9 }, (_, i) => (
        <span key={i} className="mos-tile" style={{ ["--tile-order" as string]: i }} />
      ))}
      <span className="gold-tile" />
    </span>
  );
}
