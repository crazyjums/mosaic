import { useState } from "react";

// 图片块：点击放大查看。第一阶段只有 base64 内联图片，url 字段为以后预留。
export default function ImageBlock({ mediaType, data, url }: { mediaType: string; data?: string; url?: string }) {
  const [zoom, setZoom] = useState(false);
  const src = url ?? `data:${mediaType};base64,${data ?? ""}`;
  return (
    <>
      <img
        src={src}
        alt="模型输出的图片"
        className="max-h-72 cursor-zoom-in rounded-lg border border-line"
        onClick={() => setZoom(true)}
      />
      {zoom && (
        <div className="fixed inset-0 z-50 flex cursor-zoom-out items-center justify-center bg-black/70 p-8" onClick={() => setZoom(false)}>
          <img src={src} alt="" className="max-h-full max-w-full rounded-lg" style={{ imageRendering: "pixelated" }} />
        </div>
      )}
    </>
  );
}
