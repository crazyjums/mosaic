#!/usr/bin/env python3
"""Capture current Mosaic UI from the real React browser-demo (fake data only).

Requires: pip install playwright; python -m playwright install chromium
Start Vite first: (cd frontend && npm ci && npm run dev -- --host 127.0.0.1).
Never use real user sessions or API keys for public README screenshots.
"""
from __future__ import annotations

import argparse
from pathlib import Path

from playwright.sync_api import sync_playwright

OUT = Path(__file__).resolve().parents[1] / "docs" / "images"

def capture(url: str) -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=True, args=["--no-sandbox"])
        page = browser.new_page(
            viewport={"width": 1600, "height": 900},
            device_scale_factor=1,
            locale="zh-CN",
            color_scheme="light",
            reduced_motion="reduce",
        )
        page.goto(url, wait_until="networkidle", timeout=60000)
        page.get_by_role("button", name="＋ 新会话").wait_for(timeout=20000)
        page.screenshot(path=str(OUT / "readme-workspace.png"), animations="disabled")

        # Replayed fake provider events show the actual agent UI without
        # accessing a local workspace or sending requests to an external model.
        page.get_by_role("textbox", name="消息输入框").fill("帮我制作一个示例网页，并说明实现步骤")
        page.get_by_role("button", name="发送").click()
        # Permit only interactions in the local fake/demo instance.
        for _ in range(35):
            for label in ["允许一次", "允许", "批准", "同意"]:
                btn = page.get_by_role("button", name=label, exact=True)
                if btn.count() and btn.first.is_visible():
                    btn.first.click()
                    break
            if "完成" in page.locator("body").inner_text():
                break
            page.wait_for_timeout(400)
        page.screenshot(path=str(OUT / "readme-chat.png"), animations="disabled")

        # Capture both context / artifacts to match the current fixed-width
        # right panel, not the old pre-1.7.8 sidebar.
        art = page.get_by_role("button", name="产物", exact=False)
        if art.count():
            art.first.click()
            page.wait_for_timeout(350)
            page.screenshot(path=str(OUT / "readme-artifacts.png"), animations="disabled")

        # The plugin center is a separate, realistic in-app UI (still fake data).
        page.get_by_role("button", name="⚙ 设置").click()
        page.get_by_role("navigation", name="设置分类").get_by_role("button", name="插件").click()
        page.wait_for_timeout(450)
        page.screenshot(path=str(OUT / "readme-plugins.png"), animations="disabled")
        browser.close()

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://127.0.0.1:5173")
    capture(parser.parse_args().url)
