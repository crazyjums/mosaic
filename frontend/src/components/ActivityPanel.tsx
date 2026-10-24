import type { AgentNode } from "../store/reducer";
import { useChat } from "../store/chatStore";

// 预留：子 agent 树和后台任务面板。第一阶段后端不会产生这些事件，所以面板通常不显示；
// reducer 已经能处理 subagent_* / task_* 事件，后端接入后这里会自动出现内容。
export default function ActivityPanel() {
  const agents = useChat((s) => s.agents);
  const tasks = useChat((s) => s.tasks);
  const agentList = Object.values(agents);
  const taskList = Object.values(tasks);
  if (agentList.length === 0 && taskList.length === 0) return null;

  const children = (parent: string) => agentList.filter((a) => a.parentId === parent);
  const renderNode = (a: AgentNode, depth: number) => (
    <div key={a.id}>
      <div className="flex items-center gap-1 py-0.5" style={{ paddingLeft: depth * 12 }}>
        <span className={a.status === "running" ? "text-accent" : "text-muted"}>●</span>
        <span className="truncate">{a.name}</span>
        <span className="text-muted">· {a.status}</span>
      </div>
      {children(a.id).map((c) => renderNode(c, depth + 1))}
    </div>
  );

  return (
    <aside className="w-64 shrink-0 overflow-y-auto border-l border-line bg-panel p-3 text-xs">
      {agentList.length > 0 && (
        <section>
          <div className="mb-1 font-semibold">子 agent</div>
          <div className="py-0.5">main</div>
          {children("main").map((a) => renderNode(a, 1))}
        </section>
      )}
      {taskList.length > 0 && (
        <section className="mt-4">
          <div className="mb-1 font-semibold">后台任务</div>
          {taskList.map((t) => (
            <details key={t.id} className="mb-2">
              <summary className="cursor-pointer">
                {t.title} <span className="text-muted">· {t.status}</span>
              </summary>
              <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap rounded bg-panel-2 p-2 font-mono">{t.log}</pre>
            </details>
          ))}
        </section>
      )}
    </aside>
  );
}
