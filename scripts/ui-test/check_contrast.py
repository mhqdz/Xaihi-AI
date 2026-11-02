#!/usr/bin/env python3
"""文字对比度审计（WCAG AA）。

正文阈值 4.5:1；大字（>=24px，或 >=18.66px 且 bold）阈值 3:1。
沿父链找第一个不透明背景色，跳过渐变背景图（算不准的不硬算，单独标出来）。
结果按「元素类名 + 颜色」聚合，避免同一个 class 刷屏。
"""
import sys, os, time, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"

JS = """
const parse = s => { const m = s.match(/[\\d.]+/g); return m ? m.map(Number) : null; };
const lin = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
const lum = p => 0.2126 * lin(p[0]) + 0.7152 * lin(p[1]) + 0.0722 * lin(p[2]);
const ratio = (a, b) => { const l1 = lum(a), l2 = lum(b);
  const hi = Math.max(l1, l2), lo = Math.min(l1, l2); return (hi + 0.05) / (lo + 0.05); };

const bgOf = el => {
  let e = el;
  while (e) {
    const cs = getComputedStyle(e);
    if (cs.backgroundImage !== 'none') return null;          // 背景是图/渐变，算不准
    const p = parse(cs.backgroundColor);
    if (p && (p.length < 4 || p[3] > 0.5)) return p.slice(0, 3);
    e = e.parentElement;
  }
  return [255, 255, 255];
};

const own = el => [...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim());
const skip = e => e.closest('.rail, .scale, .contour-a, .contour-b');
const seen = new Map();
for (const el of document.querySelectorAll('body *')) {
  if (!own(el) || skip(el)) continue;
  const cs = getComputedStyle(el);
  if (cs.visibility === 'hidden' || cs.display === 'none' || parseFloat(cs.opacity) < 0.5) continue;
  const r = el.getBoundingClientRect();
  if (r.width < 2 || r.height < 2) continue;
  const fg = parse(cs.color); if (!fg) continue;
  const bg = bgOf(el); if (!bg) continue;
  const size = parseFloat(cs.fontSize);
  const bold = parseInt(cs.fontWeight, 10) >= 700;
  const large = size >= 24 || (size >= 18.66 && bold);
  const need = large ? 3.0 : 4.5;
  const cr = ratio(fg.slice(0, 3), bg);
  if (cr + 0.01 < need) {
    const key = (el.className || el.tagName) + '|' + cs.color + '|' + Math.round(size);
    const prev = seen.get(key);
    const txt = el.textContent.trim().slice(0, 24);
    if (!prev) seen.set(key, { cls: String(el.className || el.tagName).slice(0, 40),
        color: cs.color, bg: `rgb(${bg.join(',')})`, size: Math.round(size), need,
        cr: Math.round(cr * 100) / 100, n: 1, sample: txt });
    else prev.n++;
  }
}
return [...seen.values()].sort((a, b) => a.cr - b.cr);
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
    s.get(BASE + path); time.sleep(2.2)
    if prep:
        s.js(prep + "; return 1"); time.sleep(1.0)
    rows = s.js(JS)
    print(f"\n── {label}：{len(rows)} 组不达标")
    for r in rows:
        print(f"   {r['cr']:>5} / {r['need']}  {r['cls'][:34]:<34} {r['size']:>2}px  "
              f"{r['color']:<20} on {r['bg']:<18} ×{r['n']:<2} {r['sample']}")
    s.close()
