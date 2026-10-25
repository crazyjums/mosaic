import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createReorderBuffer } from "./reorder";

describe("createReorderBuffer", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const setup = () => {
    const got: number[] = [];
    const push = createReorderBuffer<{ seq: number }>((e) => got.push(e.seq), 100);
    return { got, push: (...seqs: number[]) => seqs.forEach((seq) => push({ seq })) };
  };

  it("reorders the first events and then delivers in-order events immediately", () => {
    const { got, push } = setup();
    push(93, 92); // 第一条事件到达时先等一下，看看有没有更早的
    expect(got).toEqual([]);
    vi.advanceTimersByTime(100);
    expect(got).toEqual([92, 93]);
    push(94);
    expect(got).toEqual([92, 93, 94]); // 按序到达的立即交付
  });

  it("holds early events until the gap is filled", () => {
    const { got, push } = setup();
    push(1);
    vi.advanceTimersByTime(100);
    push(3, 4);
    expect(got).toEqual([1]);
    push(2);
    expect(got).toEqual([1, 2, 3, 4]);
  });

  it("gives up on a missing event after the wait", () => {
    const { got, push } = setup();
    push(1);
    vi.advanceTimersByTime(100);
    push(3);
    vi.advanceTimersByTime(100);
    expect(got).toEqual([1, 3]);
    push(2); // 迟到的照常交付
    push(4);
    expect(got).toEqual([1, 3, 2, 4]);
  });
});
