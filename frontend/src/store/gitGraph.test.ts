import { describe, expect, it } from "vitest";
import { layoutGraph } from "./gitGraph";

const c = (hash: string, ...parents: string[]) => ({ hash, parents });

describe("layoutGraph", () => {
  it("空列表", () => {
    expect(layoutGraph([])).toEqual({ rows: [], lanes: 0 });
  });

  it("线性历史只占一条车道、同一种颜色", () => {
    const g = layoutGraph([c("c", "b"), c("b", "a"), c("a")]);
    expect(g.lanes).toBe(1);
    expect(g.rows.map((r) => r.lane)).toEqual([0, 0, 0]);
    expect(new Set(g.rows.map((r) => r.color)).size).toBe(1);
    // 根提交下面不再有线
    expect(g.rows[2].lines.filter((l) => l.toY === 1)).toHaveLength(0);
    // 中间的提交：上面一条进来、下面一条出去
    expect(g.rows[1].lines).toEqual([
      { from: 0, fromY: 0, to: 0, toY: 0.5, color: g.rows[1].color },
      { from: 0, fromY: 0.5, to: 0, toY: 1, color: g.rows[1].color },
    ]);
  });

  it("分支与合并：合并提交向两条车道各连一条线，分叉点处两条车道汇合", () => {
    //   m (main) ← 合并 f
    //   |\
    //   | f (feature)
    //   |/
    //   b
    const g = layoutGraph([c("m", "b", "f"), c("f", "b"), c("b", "a"), c("a")]);
    expect(g.lanes).toBe(2);
    expect(g.rows[0].lane).toBe(0);
    // 合并行：向车道 0（b）和车道 1（f）各一条下行线
    const down = g.rows[0].lines.filter((l) => l.fromY === 0.5);
    expect(down.map((l) => l.to).sort()).toEqual([0, 1]);
    // f 在车道 1
    expect(g.rows[1].lane).toBe(1);
    // 车道 0 在 f 这一行是穿过的竖线（b 还没到）
    expect(g.rows[1].lines.some((l) => l.from === 0 && l.to === 0 && l.fromY === 0 && l.toY === 1)).toBe(true);
    // f 的父提交 b 已有车道 0 在等：f 这一行向车道 0 斜着汇过去，车道 1 随之结束
    expect(g.rows[1].lines.some((l) => l.fromY === 0.5 && l.from === 1 && l.to === 0 && l.toY === 1)).toBe(true);
    const b = g.rows[2];
    expect(b.lane).toBe(0);
    expect(b.lines.filter((l) => l.toY === 0.5).map((l) => l.from)).toEqual([0]);
    // 合并进来的分支用不同于主干的颜色
    expect(g.rows[1].color).not.toBe(g.rows[0].color);
    // 汇合之后只剩一条车道
    expect(g.rows[3].lane).toBe(0);
  });

  it("分支尖端（没人期望它）新开车道，并能复用腾出的车道", () => {
    // t1、t2 是两个互不相干的分支尖端，各自接到 a
    const g = layoutGraph([c("t1", "a"), c("t2", "a"), c("a")]);
    expect(g.rows.map((r) => r.lane)).toEqual([0, 1, 0]);
    expect(g.lanes).toBe(2);
    // 复用：x 结束后，y 可以占用它腾出的车道 0
    const h = layoutGraph([c("x"), c("y")]);
    expect(h.rows.map((r) => r.lane)).toEqual([0, 0]);
    expect(h.rows[0].color).not.toBe(h.rows[1].color);
  });

  it("第一个父提交已被别的车道期望：连过去，本车道结束", () => {
    // y 和 x 都以 a 为父：x 先出现占车道 0，y 在车道 1，a 之后两条汇合
    const g = layoutGraph([c("x", "a"), c("y", "a"), c("a")]);
    const yRow = g.rows[1];
    expect(yRow.lane).toBe(1);
    const down = yRow.lines.filter((l) => l.fromY === 0.5);
    expect(down).toHaveLength(1);
    expect(down[0].to).toBe(0); // 连向车道 0 上等待 a 的那条线
    expect(g.rows[2].lane).toBe(0);
  });

  it("章鱼合并（三个父提交）", () => {
    const g = layoutGraph([c("m", "a", "b", "c"), c("a"), c("b"), c("c")]);
    expect(g.rows[0].lines.filter((l) => l.fromY === 0.5).map((l) => l.to).sort()).toEqual([0, 1, 2]);
    expect(g.lanes).toBe(3);
  });

  it("分页：父提交不在已加载的列表里时，车道一直延伸到底", () => {
    const g = layoutGraph([c("b", "a"), c("c", "b")]); // a 还没加载
    expect(g.rows[0].lines.some((l) => l.fromY === 0.5 && l.toY === 1)).toBe(true);
  });

  it("分页布局与一次性布局一致", () => {
    const all = [c("m", "b", "f"), c("f", "b"), c("b", "a"), c("a")];
    expect(layoutGraph(all).rows.slice(0, 2)).toEqual(layoutGraph(all.slice(0, 2)).rows);
  });
});
