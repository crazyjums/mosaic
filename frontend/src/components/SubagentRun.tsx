import { useMemo, useState } from "react";
import { useChat } from "../store/chatStore";
import type { AssistantItem } from "../store/reducer";
import AssistantMessage from "./AssistantMessage";
import { t } from "../i18n";

// 子 agent 的过程：折叠在派出它的 task 工具卡片下面。
//
// 为什么折叠：派子 agent 就是为了不让大量中间输出挤进主对话。运行中默认展开（能看到它在做什么），
// 结束后默认收起，只留一行“子 agent X · 完成 · N 步”，需要时再点开看。
export default function SubagentRun({ callId }: { callId: string }) {
  const agent = useChat((s) => Object.values(s.agents).find((a) => a.callId === callId));
  // 选择器只取 timeline 的引用（store 不变时引用不变），过滤放在 useMemo 里：
  // 在选择器里 filter 每次都返回新数组，Zustand 5 会认为快照一直在变而反复重渲染。
  const timeline = useChat((s) => s.timeline);
  const agentId = agent?.id;
  const items = useMemo(
    () => (agentId ? timeline.filter((it): it is AssistantItem => it.kind === "assistant" && it.agentId === agentId) : []),
    [timeline, agentId],
  );
  const running = agent?.status === "running";
  const [open, setOpen] = useState<boolean | null>(null); // null = 跟随状态（运行中展开、结束后收起）
  if (!agent) return null;
  const shown = open ?? running;
  const label = running ? t("运行中") : agent.status === "completed" ? t("完成") : agent.status === "cancelled" ? t("已停止") : t("失败");
  return (
    <div className="border-t border-line bg-panel-2/50 px-3 py-1.5 text-xs">
      <button className="flex w-full items-center gap-2 text-left text-muted hover:text-fg" onClick={() => setOpen(!shown)} aria-expanded={shown}>
        <span className={`transition-transform ${shown ? "rotate-90" : ""}`}>▶</span>
        <span className="font-medium text-fg">{t("子 agent")} {agent.name}</span>
        <span>· {label}</span>
        <span>· {t("{n} 步", { n: String(items.length) })}</span>
        {running && <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-accent" />}
      </button>
      {shown && (
        <div className="mt-2 space-y-2 border-l-2 border-accent/40 pl-3">
          {items.length === 0 && <div className="text-muted">{t("等待子 agent 开始…")}</div>}
          {items.map((it) => (
            <AssistantMessage key={it.id} item={it} />
          ))}
        </div>
      )}
    </div>
  );
}
