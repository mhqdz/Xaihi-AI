#!/usr/bin/env python3
"""点击目标尺寸审计。
WCAG 2.5.8 的最小目标是 24×24 CSS px。列出所有可点元素里小于它的，
按面积从小到大排，附元素与文字，方便判断是真问题还是刻意的小控件。
"""
import sys, os, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"

# 刻意做小、不该算问题的：标尺刻度、拖拽把手、折叠三角
EXCLUDE = (".scale i", ".edge", "summary")

JS = """
const ex = %s;
const hit = e => ex.some(s => e.matches(s));
const els = [...document.querySelectorAll('button,a,[onclick],[role=button],select,summary,input[type=checkbox],.item')];
const out = [];
for (const e of els) {
  if (hit(e)) continue;
  // checkbox/radio 外面套着 label 的，真实点击区是 label，别把 14×14 的框本身当问题
  const lab = e.tagName === 'INPUT' && e.closest('label');
  if (lab && lab !== e) continue;
  const r = e.getBoundingClientRect();
  if (r.width < 1 || r.height < 1) continue;
  const cs = getComputedStyle(e);
  if (cs.visibility === 'hidden' || cs.display === 'none' || cs.opacity === '0') continue;
  if (r.width < 24 || r.height < 24) {
    out.push([e.tagName.toLowerCase() + '.' + String(e.className || '').split(' ').join('.'),
              Math.round(r.width), Math.round(r.height),
              (e.textContent || e.getAttribute('aria-label') || e.title || '').trim().slice(0, 18)]);
  }
}
out.sort((a, b) => a[1] * a[2] - b[1] * b[2]);
return out;
""" % (repr(list(EXCLUDE)),)

def run(label, w, h, path, prep=None):
    s = Session(w, h)
    req("POST", f"/session/{s.id}/window/rect", {"width": w, "height": h})
    s.get(BASE + path); time.sleep(2.0)
    if prep:
        s.js(prep + "; return 1"); time.sleep(1.0)
    rows = s.js(JS)
    print(f"\n── {label}（{w}×{h} {path}）{len(rows)} 个小于 24px")
    for cls, cw, ch, txt in rows:
        print(f"   {cw:>3}×{ch:<3}  {cls[:46]:<46} {txt}")
    s.close()
    return rows

allrows = []
allrows += run("会话页", 1600, 1000, "/?conv=51")
allrows += run("空状态", 1600, 1000, "/")
allrows += run("设置", 1600, 1000, "/?settings=1&conv=51")
allrows += run("模型详情", 1600, 1000, "/?settings=1&conv=51",
               "document.querySelector('.col-list .item.col').click()")
allrows += run("窄屏会话", 900, 1000, "/?conv=51")

print(f"\n=== 合计 {len(allrows)} 处 ===")
sys.exit(0)
