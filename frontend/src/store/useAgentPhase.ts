// 把 agentPhase.livePhase 接到聊天状态上，并在一轮结束时给出一次短暂的"完成 / 出错"反馈。
import { useEffect, useRef, useState } from "react";
import type { MosaicState } from "../components/MosaicMark";
import { livePhase } from "./agentPhase";
import { useChat } from "./chatStore";

const FEEDBACK_MS = 3000;

export function useAgentPhase(): MosaicState {
  const live = useChat((s) => livePhase(s));
  // 只选取原始值（不要在选择器里构造新对象，否则每次都"变化"，会无限重渲染）
  const endId = useChat((s) => {
    const last = s.timeline[s.timeline.length - 1];
    return last?.kind === "turn_end" && !last.replay ? last.id : "";
  });
  const endReason = useChat((s) => {
    const last = s.timeline[s.timeline.length - 1];
    return last?.kind === "turn_end" ? last.reason : "";
  });
  const [feedback, setFeedback] = useState<MosaicState | null>(null);
  const seen = useRef<string>(endId);

  // 一轮结束的那一刻（出现新的 turn_end）给出一次反馈：完成 → done；出错 → problem；用户取消 → 不给反馈。
  useEffect(() => {
    if (!endId || endId === seen.current) return;
    seen.current = endId;
    if (endReason === "cancelled") return;
    setFeedback(endReason === "completed" ? "done" : "problem");
    const timer = setTimeout(() => setFeedback(null), FEEDBACK_MS);
    return () => clearTimeout(timer);
  }, [endId, endReason]);

  return live !== "static" ? live : (feedback ?? "static");
}
