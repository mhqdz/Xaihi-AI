#!/usr/bin/env python3
"""可点元素的光标检查。

`<button>` 的默认 cursor 是 default 而不是 pointer，所以只要样式里漏写，
用户悬停时就不会得到"这里能点"的反馈 —— 尤其和旁边有 pointer 的元素放一起时很明显。
<a href> 默认就是 pointer，不报。
"""
import sys, os, time, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"

JS = """
const seen = new Map();
for (const e of document.querySelectorAll('button,[onclick],[role=button],label,input[type=checkbox],summary')) {
  const r = e.getBoundingClientRect();
  if (r.width < 4 || r.height < 4) continue;
  const cs = getComputedStyle(e);
  if (cs.visibility === 'hidden' || cs.display === 'none') continue;
  if (cs.cursor === 'pointer') continue;
  const key = (e.tagName + '.' + String(e.className || '')).slice(0, 50);
  const txt = (e.textContent || e.getAttribute('aria-label') || e.title || '').trim().slice(0, 18);
  const prev = seen.get(key);
  if (prev) prev.n++;
  else seen.set(key, {el: key, cursor: cs.cursor, n: 1, txt});
}
return [...seen.values()].sort((a, b) => b.n - a.n);
"""

for label, w, h, path, prep in (
    ("会话页", 1600, 1000, "/?conv=51", None),
    ("空状态", 1600, 1000, "/", None),
    ("设置", 1600, 1000, "/?settings=1&conv=51", None),
    ("模型详情", 1600, 1000, "/?settings=1&conv=51",
     "document.querySelector('.col-list .item.col').click()"),
):
    s = Session(w, h)
    req("POST", f"/session/{s.id}/window/rect", {"width": w, "height": h})
    s.get(BASE + path); time.sleep(2.1)
    if prep:
        s.js(prep + "; return 1"); time.sleep(1.0)
    rows = s.js(JS)
    print(f"\n── {label}：{len(rows)} 组没有 pointer")
    for r in rows:
        print(f"   {r['cursor']:<10} ×{r['n']:<3} {r['el'][:48]:<48} {r['txt']}")
    s.close()
