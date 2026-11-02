#!/usr/bin/env python3
"""生成交付用截图到 docs/screenshots/。

注意 Firefox headless 的最小窗口宽度约 500px，再窄会按 500 渲染。
"""
import sys, os, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "../../docs/screenshots")

SHOTS = [
    ("01-conversation", 1600, 1000, "/?conv=51", None),
    ("02-empty", 1600, 1000, "/", None),
    ("03-settings", 1600, 1000, "/?settings=1&conv=51", None),
    ("04-model-detail", 1600, 1000, "/?settings=1&conv=51",
     "document.querySelector('.col-list .item.col').click()"),
    ("05-narrow", 900, 1000, "/?conv=51", None),
    ("06-mobile", 500, 900, "/?conv=51", None),
]

os.makedirs(OUT, exist_ok=True)
for name, w, h, path, prep in SHOTS:
    s = Session(w, h)
    req("POST", f"/session/{s.id}/window/rect", {"width": w, "height": h})
    s.get(BASE + path); time.sleep(2.2)
    if prep:
        s.js(prep + "; return 1"); time.sleep(1.0)
    p = os.path.join(OUT, name + ".png")
    s.shot(p)
    s.close()
    print("  saved", os.path.normpath(p))
