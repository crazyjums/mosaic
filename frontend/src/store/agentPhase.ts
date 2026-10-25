// Agent 的"阶段"：Mosaic Logo 和执行状态卡片据此显示不同的动画。
//
// 阶段完全由真实的聊天状态推导，不用定时器模拟进度：
//   有权限请求在等你 / 本轮以错误结束  → problem（需要你处理）
//   运行中，还没有任何输出              → start
//   运行中，已经有思考 / 工具调用 / 文字 → thinking
//   刚刚完成一轮                        → done（只播放一次，约 3 秒后回到静止）
//   其他                                → static
// 唯一的计时器只用来让"done / problem 的反馈"在播放完后回到静止（见 useAgentPhase），它不决定任何执行状态。
// 本文件是纯函数，不依赖 store 和桥接层，方便单测。

import type { MosaicState } from "../components/MosaicMark";
import type { ChatState } from "./reducer";

/** 不含"反馈保持"的瞬时阶段，纯函数，方便测试。 */
export function livePhase(s: Pick<ChatState, "running" | "permissions" | "timeline">): MosaicState {
  if (s.permissions.length > 0) return "problem";
  if (s.running) {
    const last = s.timeline[s.timeline.length - 1];
    return last?.kind === "assistant" && last.blocks.length > 0 ? "thinking" : "start";
  }
  return "static";
}
