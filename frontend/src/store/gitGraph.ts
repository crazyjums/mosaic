// Git 分支图的“车道”布局：把按时间倒序排列的提交（带父提交）排成若干条竖直车道，
// 并算出每一行要画的线段。纯函数，不依赖 DOM，渲染见 components/GitGraph.tsx。
//
// 做法是 git log --graph 同款：从上往下扫描，维护“车道 → 下一个期望看到的提交”。
//   - 提交出现在期望它的车道上（有多条车道期望它 = 分支在这里汇合）；没有车道期望它（分支的尖端）就新开一条；
//   - 提交的第一个父提交继续沿用这条车道，其余父提交（合并进来的分支）各占一条车道，已有车道在等它就连过去；
//   - 车道上没人再期望的就腾出来，之后可以复用。
//
// 分页加载时每次对“已加载的全部提交”重新布局，结果和一次性加载完全一致。

export interface GraphInput {
  hash: string;
  parents: string[];
}

/** 一段线：从 (from 车道, fromY) 到 (to 车道, toY)；Y 取 0（行顶）、0.5（提交圆点）、1（行底）。 */
export interface GraphLine {
  from: number;
  fromY: 0 | 0.5;
  to: number;
  toY: 0.5 | 1;
  /** 颜色序号，渲染时对调色板取模 */
  color: number;
}

export interface GraphRow {
  /** 提交圆点所在的车道 */
  lane: number;
  color: number;
  lines: GraphLine[];
}

export interface GraphLayout {
  rows: GraphRow[];
  /** 用到的车道数（最宽的一行） */
  lanes: number;
}

export function layoutGraph(commits: GraphInput[]): GraphLayout {
  const lanes: (string | null)[] = [];
  const colors: number[] = [];
  let nextColor = 0;
  let maxLane = 0;

  const freeLane = (): number => {
    const i = lanes.indexOf(null);
    if (i >= 0) return i;
    lanes.push(null);
    return lanes.length - 1;
  };
  const claim = (i: number, hash: string) => {
    lanes[i] = hash;
    colors[i] = nextColor++;
  };

  const rows: GraphRow[] = commits.map((c) => {
    const before = lanes.slice();
    const beforeColors = colors.slice();
    const matches: number[] = [];
    before.forEach((h, i) => {
      if (h === c.hash) matches.push(i);
    });

    let lane: number;
    if (matches.length > 0) {
      lane = matches[0];
    } else {
      lane = freeLane();
      colors[lane] = nextColor++;
    }
    const color = colors[lane];
    const lines: GraphLine[] = [];

    // 行的上半部分：每条在途的车道，要么汇入这个提交，要么原样穿过
    before.forEach((h, i) => {
      if (h === null) return;
      if (h === c.hash) lines.push({ from: i, fromY: 0, to: lane, toY: 0.5, color: beforeColors[i] });
      else lines.push({ from: i, fromY: 0, to: i, toY: 1, color: beforeColors[i] });
    });
    for (const m of matches) lanes[m] = null;

    // 行的下半部分：连向父提交
    const [first, ...rest] = c.parents;
    if (first !== undefined) {
      const existing = lanes.indexOf(first);
      if (existing >= 0) {
        // 第一个父提交已经有别的车道在等它：连过去，本车道结束
        lines.push({ from: lane, fromY: 0.5, to: existing, toY: 1, color: colors[existing] });
      } else {
        lanes[lane] = first;
        colors[lane] = color;
        lines.push({ from: lane, fromY: 0.5, to: lane, toY: 1, color });
      }
    }
    for (const p of rest) {
      let idx = lanes.indexOf(p);
      if (idx < 0) {
        idx = freeLane();
        claim(idx, p);
      }
      lines.push({ from: lane, fromY: 0.5, to: idx, toY: 1, color: colors[idx] });
    }

    for (const l of lines) maxLane = Math.max(maxLane, l.from, l.to);
    maxLane = Math.max(maxLane, lane);
    return { lane, color, lines };
  });

  return { rows, lanes: commits.length === 0 ? 0 : maxLane + 1 };
}
