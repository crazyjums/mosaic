import { useEffect, useLayoutEffect, useRef } from "react";
import { useChat } from "../store/chatStore";
import { useSettings } from "../store/settingsStore";
import type { AssistantItem, TimelineItem } from "../store/reducer";
import TurnView from "./TurnView";
import UserBubble from "./UserBubble";
import ScholarThinking from "./ScholarThinking";
import WorkspacePicker from "./WorkspacePicker";
import { t } from "../i18n";

// 只存中文原文，渲染时再翻译：模块加载时语言还没确定。
const REASON: Record<string, string> = {
  completed: "完成",
  max_steps: "达到最大步数",
  cancelled: "已取消",
  error: "出错",
};

// 消息流。自动滚动策略：只有用户本来就在底部附近时才跟随滚动，
// 否则用户往上翻看历史时会被新内容不停地拽回底部。
export default function MessageList() {
  const timeline = useChat((s) => s.timeline);
  const running = useChat((s) => s.running);
  const backendKind = useChat((s) => s.backendKind);
  const isDemoModel = useChat((s) => s.profiles.find((p) => p.name === s.provider)?.type === "fake");
  const sessionId = useChat((s) => s.sessionId);
  const earlier = useChat((s) => s.earlier);
  const loadingEarlier = useChat((s) => s.loadingEarlier);
  const prependTick = useChat((s) => s.prependTick);
  const loadEarlier = useChat((s) => s.loadEarlier);
  const noProvider = useChat((s) => s.backendKind === "wails" && !s.provider);
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  // 接入更早的历史前的内容高度：接入后用它补偿滚动位置，避免用户正在看的内容被顶走。
  const heightBefore = useRef(0);

  // 切换会话后回到"跟随底部"：上一个会话可能停在中间。
  useEffect(() => {
    stick.current = true;
  }, [sessionId]);

  useEffect(() => {
    const el = ref.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [timeline]);

  // 更早的历史接到前面之后，让可见内容保持不动。必须在浏览器绘制之前完成，所以用 layout effect。
  useLayoutEffect(() => {
    const el = ref.current;
    if (el && prependTick > 0) el.scrollTop += el.scrollHeight - heightBefore.current;
  }, [prependTick]);

  const more = () => {
    const el = ref.current;
    if (!el || loadingEarlier) return;
    heightBefore.current = el.scrollHeight;
    stick.current = false;
    void loadEarlier();
  };

  const onScroll = () => {
    const el = ref.current;
    if (!el) return;
    stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    // 滚到顶部附近自动加载上一页；按钮是兜底（内容没填满一屏、没法滚动时用）。
    if (earlier > 0 && el.scrollTop < 120) more();
  };

  return (
    // overflow-anchor: none —— 浏览器自己的"滚动锚定"会在上方插入内容时偷偷调整 scrollTop，
    // 和下面手动补偿叠加就会多跳一次（WebView2 / Chromium 里会发生）；关掉它，位置只由我们的补偿决定。
    // onWheel：已经在最顶部时继续向上滚不会再触发 scroll 事件，所以单独监听滚轮。
    <div
      ref={ref}
      onScroll={onScroll}
      onWheel={(e) => e.deltaY < 0 && earlier > 0 && (ref.current?.scrollTop ?? 1) <= 0 && more()}
      style={{ overflowAnchor: "none" }}
      className="min-w-0 flex-1 overflow-y-auto"
    >
      <div className="mx-auto max-w-[var(--content-width)] space-y-5 px-6 py-6">
        {earlier > 0 && (
          <div className="flex justify-center">
            <button className="btn text-xs" onClick={more} disabled={loadingEarlier}>
              {loadingEarlier ? t("加载中…") : t("加载更早的对话（还有 {n} 轮）", { n: earlier })}
            </button>
          </div>
        )}
        {timeline.length === 0 && <Empty demo={backendKind === "demo"} demoModel={isDemoModel} noProvider={noProvider} />}
        {group(timeline).map((it, idx, all) => {
          if (Array.isArray(it)) {
            // 只有最后一组、且这一轮还在运行时算"进行中"
            const live = running && idx === all.length - 1;
            return <TurnView key={it[0].id} items={it} live={live} />;
          }
          switch (it.kind) {
            case "user":
              return (
                <div key={it.id} className="flex justify-end">
                  <UserBubble text={it.text} attachments={it.attachments} />
                </div>
              );
            case "error":
              return (
                <div key={it.id} role="alert" className={`flex gap-2 rounded-ctl border px-3 py-2.5 text-sm ${it.fatal ? "border-danger/40 bg-danger/10 text-danger" : "border-warn/40 bg-warn/10 text-warn"}`}>
                  <span aria-hidden="true">{it.fatal ? "⛔" : "⚠"}</span>
                  <div className="min-w-0 break-words">
                    <span className="mr-2 font-mono text-xs opacity-70">{it.code}</span>
                    {it.message}
                  </div>
                </div>
              );
            case "notice":
              return (
                <div key={it.id} className="text-center text-xs text-muted">
                  {it.text}
                </div>
              );
            case "turn_end":
              if (it.replay) return null;
              return (
                <div key={it.id} className="flex items-center gap-3 text-[11px] text-muted">
                  <div className="h-px flex-1 bg-line" />
                  {t("{reason} · {steps} 步 · 入 {input} / 出 {output} tokens", {
                    reason: REASON[it.reason] ? t(REASON[it.reason]) : it.reason,
                    steps: it.steps,
                    input: it.usage.input_tokens,
                    output: it.usage.output_tokens,
                  })}
                  <div className="h-px flex-1 bg-line" />
                </div>
              );
          }
        })}
        {/* 刚发出消息、模型还没开始响应 */}
        {running && timeline[timeline.length - 1]?.kind === "user" && <ScholarThinking />}
      </div>
    </div>
  );
}

/** 把连续的 assistant 消息合成一组（一轮里模型的全部输出），其他条目原样保留。 */
function group(timeline: TimelineItem[]): (TimelineItem | AssistantItem[])[] {
  const out: (TimelineItem | AssistantItem[])[] = [];
  for (const it of timeline) {
    const prev = out[out.length - 1];
    if (it.kind === "assistant") {
      if (Array.isArray(prev)) prev.push(it);
      else out.push([it]);
    } else out.push(it);
  }
  return out;
}

function Empty({ demo, demoModel, noProvider }: { demo: boolean; demoModel: boolean; noProvider: boolean }) {
  const openSettings = useSettings((s) => s.show);
  return (
    <div className="mt-24 text-center text-muted">
      <div className="text-2xl font-semibold text-fg">Mosaic</div>
      <p className="mt-2 text-sm">{t("自己实现的 agent harness · 思考、工具调用、权限确认全过程可视化")}</p>
      {/* 新会话先确认工作区：一个会话通常对应一个代码仓库 */}
      <WorkspacePicker variant="card" />
      {demo && (
        <p className="mx-auto mt-6 max-w-md rounded-lg border border-line bg-panel px-4 py-3 text-xs">
          {t("当前是浏览器演示模式：没有连接 Go 后端，回放的是 fake provider 录制的事件流。随便输入一句话试试；在 Wails 桌面端中会连接真实内核。")}
        </p>
      )}
      {noProvider && (
        <div className="mx-auto mt-6 max-w-md rounded-lg border border-line bg-panel px-4 py-3 text-xs">
          {t("还没有配置模型厂商。添加一个（填写 Base URL 和 API Key），或登录 ChatGPT 账号后就可以开始对话。")}
          <button className="mt-2 block w-full rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-on-accent hover:opacity-90" onClick={() => void openSettings("providers")}>
            {t("配置模型厂商")}
          </button>
        </div>
      )}
      {!demo && demoModel && (
        <div className="mx-auto mt-6 max-w-md rounded-lg border border-line bg-panel px-4 py-3 text-xs">
          {t("当前使用的是演示模型（按固定剧本回复，不调用真实模型）。")}
          <button className="mt-2 block w-full rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-on-accent hover:opacity-90" onClick={() => void openSettings("providers")}>
            {t("配置真实模型（Base URL + API Key）")}
          </button>
        </div>
      )}
    </div>
  );
}
