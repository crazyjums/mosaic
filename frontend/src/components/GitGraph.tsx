import type { GraphRow } from "../store/gitGraph";

// 分支图的一行：在固定宽度的 SVG 里画这一行的车道线和提交圆点。
// 每行单独一个 SVG（高度 = 行高），上下相邻的行靠线段在 y=0 / y=ROW_H 处首尾相接。

export const ROW_H = 30;
export const LANE_W = 16;
const PAD = 10;
const R = 4.5;

/** 车道调色板：第一个跟随皮肤主题色，其余是在浅色/深色底上都足够清晰的固定色。 */
export const LANE_COLORS = ["var(--accent)", "#d9962b", "#cf5c5c", "#4f86d6", "#8a6fd0", "#35a374", "#d0689a", "#7b8c9d"];
const colorOf = (i: number) => LANE_COLORS[i % LANE_COLORS.length];

const xOf = (lane: number) => PAD + lane * LANE_W;

export function graphWidth(lanes: number): number {
  return PAD * 2 + Math.max(0, lanes - 1) * LANE_W;
}

export default function GitGraphRow({ row, lanes, merge, selected }: { row: GraphRow; lanes: number; merge: boolean; selected: boolean }) {
  const w = graphWidth(lanes);
  return (
    <svg width={w} height={ROW_H} viewBox={`0 0 ${w} ${ROW_H}`} className="shrink-0" aria-hidden="true">
      {row.lines.map((l, i) => {
        const x1 = xOf(l.from);
        const y1 = l.fromY * ROW_H;
        const x2 = xOf(l.to);
        const y2 = l.toY * ROW_H;
        // 同一车道画直线；换车道画一条平滑的 S 曲线
        const d = x1 === x2 ? `M${x1} ${y1} L${x2} ${y2}` : `M${x1} ${y1} C${x1} ${(y1 + y2) / 2 + 4} ${x2} ${(y1 + y2) / 2 - 4} ${x2} ${y2}`;
        return <path key={i} d={d} stroke={colorOf(l.color)} strokeWidth={2} fill="none" strokeLinecap="round" />;
      })}
      <circle
        cx={xOf(row.lane)}
        cy={ROW_H / 2}
        r={selected ? R + 1 : R}
        fill={merge ? "var(--panel)" : colorOf(row.color)}
        stroke={colorOf(row.color)}
        strokeWidth={merge ? 2 : selected ? 2 : 0}
      />
    </svg>
  );
}
