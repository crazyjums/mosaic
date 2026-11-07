import { Menu, Tray, nativeImage } from "electron";

// 系统托盘：关窗后会话还在后台跑时，用户靠它找回窗口（迁移阶段 4）。

export interface TrayActions {
  show: () => void;
  quickAsk: () => void;
  quit: () => void;
}

export function createTray(iconPath: string, shortcut: string | undefined, actions: TrayActions): Tray | undefined {
  let icon = nativeImage.createFromPath(iconPath);
  if (icon.isEmpty()) return undefined; // 图标没找到就不建托盘（宁可不要，也不要一个空白图标）
  icon = icon.resize({ width: 18, height: 18 });
  const tray = new Tray(icon);
  tray.setToolTip("Mosaic");
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: "显示 Mosaic", click: actions.show },
      { label: shortcut ? `快速提问（${shortcut}）` : "快速提问", click: actions.quickAsk },
      { type: "separator" },
      { label: "退出", click: actions.quit },
    ]),
  );
  // Windows / Linux 单击托盘图标直接显示窗口；macOS 习惯用菜单。
  tray.on("click", actions.show);
  return tray;
}
