import { useEffect, useState } from "react";
import { t } from "../i18n";

// 等待模型响应时的动画：一位戴金丝眼镜、博士帽的学者，托着下巴思考，头顶冒出思考泡泡。
// 收到第一个内容（思考、文本或工具调用）后，AssistantMessage 就不再渲染它。
//
// 纯 SVG + CSS 动画（样式在 index.css 的 .scholar-*），不引入图片或动画库；
// 泡泡等界面元素的颜色用 CSS 变量，跟随皮肤；系统开启"减少动态效果"时动画停止。

const SYMBOLS = ["∑", "?", "π", "∫", "λ"];

export default function ScholarThinking({ since }: { since?: number }) {
  const [now, setNow] = useState(Date.now());
  const [symbol, setSymbol] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    const sym = setInterval(() => setSymbol((i) => (i + 1) % SYMBOLS.length), 1600);
    return () => {
      clearInterval(timer);
      clearInterval(sym);
    };
  }, []);
  const secs = since ? Math.max(0, Math.floor((now - since) / 1000)) : null;

  return (
    <div className="flex items-center gap-3" role="status" aria-live="polite">
      <svg className="scholar h-24 w-24 shrink-0" viewBox="0 0 120 112" aria-hidden="true">
        {/* 思考泡泡：从小到大依次浮现，最大的里面轮换公式符号 */}
        <g className="scholar-bubbles">
          <circle className="scholar-bubble b1" cx="88" cy="40" r="2.2" />
          <circle className="scholar-bubble b2" cx="95" cy="30" r="3.4" />
          <g className="scholar-bubble b3">
            <ellipse cx="104" cy="15" rx="13" ry="10" />
            <text x="104" y="19.5" textAnchor="middle" className="scholar-symbol">
              {SYMBOLS[symbol]}
            </text>
          </g>
        </g>

        {/* 学位袍（肩膀）和白色领口 */}
        <path d="M24 112 Q26 82 60 79 Q94 82 96 112 Z" fill="#1f3a5f" />
        <path d="M52 80 L60 94 L68 80 Q60 83 52 80 Z" fill="#f8fafc" />
        <path d="M58 86 L60 94 L62 86 Z" fill="#9b2c2c" />

        {/* 头 */}
        <g className="scholar-head">
          <circle cx="39.5" cy="55" r="4" fill="#e8b98f" />
          <circle cx="80.5" cy="55" r="4" fill="#e8b98f" />
          <circle cx="60" cy="54" r="20" fill="#f2c9a0" />
          {/* 两鬓的白发 */}
          <path d="M40 50 Q39 40 46 37 Q43 45 45 52 Z" fill="#d6d9de" />
          <path d="M80 50 Q81 40 74 37 Q77 45 75 52 Z" fill="#d6d9de" />
          {/* 眉毛：一边挑起，像在琢磨 */}
          <path d="M47.5 44.5 Q52 42 56 44" stroke="#8a8f98" strokeWidth="1.6" fill="none" strokeLinecap="round" />
          <path className="scholar-brow" d="M64 43 Q68.5 40 72.5 42.5" stroke="#8a8f98" strokeWidth="1.6" fill="none" strokeLinecap="round" />
          {/* 眼睛（会眨） */}
          <g className="scholar-eyes">
            <circle cx="52" cy="51.5" r="1.7" fill="#2b2b2b" />
            <circle cx="68" cy="51.5" r="1.7" fill="#2b2b2b" />
          </g>
          {/* 金丝眼镜 */}
          <g fill="rgba(255,255,255,0.12)" stroke="#c9a227" strokeWidth="1.5">
            <circle cx="52" cy="51.5" r="6.3" />
            <circle cx="68" cy="51.5" r="6.3" />
          </g>
          <path d="M58.3 51 Q60 49.6 61.7 51" stroke="#c9a227" strokeWidth="1.3" fill="none" />
          <path d="M45.7 50.5 L41 52 M74.3 50.5 L79 52" stroke="#c9a227" strokeWidth="1.2" />
          <path className="scholar-glint" d="M48.5 48.5 L50.5 46.8" stroke="#fff" strokeWidth="1.2" strokeLinecap="round" />
          {/* 胡子和嘴 */}
          <path d="M50 63 Q60 59 70 63 Q68 73 60 74.5 Q52 73 50 63 Z" fill="#e7e9ec" />
          <path d="M55.5 63.5 Q60 65.5 64.5 63.5" stroke="#9aa0a8" strokeWidth="1.2" fill="none" strokeLinecap="round" />
          {/* 博士帽：帽檐、帽身、流苏 */}
          <path d="M42 34 Q60 41 78 34 L78 27 Q60 33 42 27 Z" fill="#111827" />
          <path d="M28 25 L60 13 L92 25 L60 37 Z" fill="#1f2937" />
          <circle cx="60" cy="25" r="1.6" fill="#c9a227" />
          <g className="scholar-tassel">
            <path d="M60 25 L86 27 L87 40" stroke="#c9a227" strokeWidth="1.3" fill="none" />
            <path d="M85 40 L89 40 L88.5 46 L85.5 46 Z" fill="#c9a227" />
          </g>
        </g>

        {/* 托着下巴的手（轻轻敲下巴） */}
        <g className="scholar-hand">
          <path d="M96 112 Q92 92 76 84 L70 90 Q84 98 86 112 Z" fill="#183150" />
          <ellipse cx="68" cy="82" rx="6.5" ry="5.5" fill="#f2c9a0" />
          <path d="M64.5 79 Q63.5 73 66 72" stroke="#e1b083" strokeWidth="3" fill="none" strokeLinecap="round" />
        </g>
      </svg>
      <div className="text-sm text-muted">
        <div className="font-medium text-fg">{t("正在思考…")}</div>
        {secs !== null && secs >= 1 && <div className="text-xs tabular-nums">{t("已等待 {n} 秒", { n: secs })}</div>}
      </div>
    </div>
  );
}
