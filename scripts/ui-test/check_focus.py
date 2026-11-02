#!/usr/bin/env python3
"""键盘焦点可见性检查。

用 WebDriver 真的按 Tab 键（而不是程序化 focus()）—— 只有这样才会触发 :focus-visible，
也是真实键盘用户走的路径。每一步记录焦点落在哪、以及它的 outline / box-shadow。
两者同时为空，说明焦点不可见。
"""
import sys, os, time, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"
TAB = "\ue004"

QUERY = """
const e = document.activeElement;
if (!e || e === document.body) return null;
const cs = getComputedStyle(e);
return {tag: e.tagName, cls: String(e.className || '').slice(0, 34),
        txt: (e.textContent || e.getAttribute('aria-label') || e.placeholder || '').trim().slice(0, 14),
        outline: cs.outlineStyle === 'none' ? 'none' : cs.outlineWidth + ' ' + cs.outlineStyle,
        shadow: cs.boxShadow === 'none' ? 'none' : cs.boxShadow.slice(0, 28),
        border: cs.borderColor};
"""

def tab(s):
    req("POST", f"/session/{s.id}/actions", {"actions": [
        {"type": "key", "id": "kb", "actions": [
            {"type": "keyDown", "value": TAB}, {"type": "keyUp", "value": TAB}]}]})
    time.sleep(0.12)

blind = []
for label, path, n in (("会话页", "/?conv=51", 12), ("设置", "/?settings=1&conv=51", 10)):
    s = Session(1600, 1000)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
    s.get(BASE + path); time.sleep(2.2)
    print(f"\n── {label}")
    for i in range(n):
        tab(s)
        r = s.js(QUERY)
        if not r:
            print(f"   {i+1:>2}  (body)"); continue
        invisible = r["outline"] == "none" and r["shadow"] == "none"
        flag = "  ← 焦点不可见" if invisible else ""
        print(f"   {i+1:>2}  {r['tag']:<8} {r['cls'][:30]:<30} {r['txt']:<14} "
              f"outline={r['outline']:<14} shadow={r['shadow'][:24]}{flag}")
        if invisible:
            blind.append((label, r["tag"], r["cls"], r["txt"]))
    s.close()

print(f"\n=== 焦点不可见的元素：{len(blind)} 处 ===")
for b in blind:
    print("  ", b)
sys.exit(1 if blind else 0)
