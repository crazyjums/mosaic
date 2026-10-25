// 设置导航的线性图标：16×16、描边 1.5、currentColor，跟随文字颜色（选中态自动变白）。
// 自己画而不是引入图标库，保持依赖精简；风格统一（圆角端点、同样的描边粗细）。

const PATHS: Record<string, React.ReactNode> = {
  general: (
    <>
      <path d="M2.5 4.5h6M11.5 4.5h2M2.5 11.5h2M7.5 11.5h6" />
      <circle cx="10" cy="4.5" r="1.5" />
      <circle cx="6" cy="11.5" r="1.5" />
    </>
  ),
  providers: <path d="M4.5 12.5a3 3 0 0 1-.4-5.97 4 4 0 0 1 7.6-.7A3.4 3.4 0 0 1 11.5 12.5Z" />,
  prompt: (
    <>
      <path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2Z" />
      <path d="M5 6.2h6M5 8.2h3.5" />
    </>
  ),
  permissions: (
    <>
      <path d="M8 2 3 3.8v3.7c0 3 2 5 5 6.5 3-1.5 5-3.5 5-6.5V3.8Z" />
      <path d="m6 8 1.6 1.6L10.2 6.6" />
    </>
  ),
  mcp: (
    <>
      <path d="M6 2.5v3M10 2.5v3M4.5 5.5h7v2.2a3.5 3.5 0 0 1-7 0Z" />
      <path d="M8 11.2V13.5" />
    </>
  ),
  appearance: (
    <>
      <circle cx="8" cy="8" r="5.5" />
      <path d="M8 2.5v11a5.5 5.5 0 0 0 0-11Z" fill="currentColor" stroke="none" opacity=".35" />
    </>
  ),
  statusline: (
    <>
      <rect x="2.5" y="3" width="11" height="10" rx="1.5" />
      <path d="M2.5 10h11M5 11.6h1.5M8 11.6h3" />
    </>
  ),
  usage: (
    <>
      <path d="M3 13V8.5M7 13V4M11 13V6.5M14 13.5H2" />
    </>
  ),
  data: (
    <>
      <ellipse cx="8" cy="4.5" rx="4.5" ry="1.8" />
      <path d="M3.5 4.5v7c0 1 2 1.8 4.5 1.8s4.5-.8 4.5-1.8v-7M3.5 8c0 1 2 1.8 4.5 1.8s4.5-.8 4.5-1.8" />
    </>
  ),
  about: (
    <>
      <circle cx="8" cy="8" r="5.5" />
      <path d="M8 7.3V11M8 5.2v.1" />
    </>
  ),
};

export default function NavIcon({ name, className = "" }: { name: string; className?: string }) {
  return (
    <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true">
      {PATHS[name] ?? PATHS.general}
    </svg>
  );
}
