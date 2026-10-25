// 弹出卡片的定位：在锚点上方，水平方向夹在窗口内，高度不超过锚点上方的空间。
// 状态栏的额度项在最右边，卡片原来向右展开，会超出屏幕；这里改成按窗口夹住。

export interface Placement {
  width: number;
  left: number;
  bottom: number;
  maxHeight: number;
}

export function placeAbove(anchor: { left: number; top: number }, viewport: { width: number; height: number }, width = 288, margin = 8): Placement {
  const w = Math.min(width, viewport.width - margin * 2);
  return {
    width: w,
    left: Math.max(margin, Math.min(anchor.left, viewport.width - w - margin)),
    bottom: viewport.height - anchor.top + margin,
    maxHeight: Math.max(160, anchor.top - margin * 2),
  };
}
