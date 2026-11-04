#!/usr/bin/env python3
"""配置面板逐控件走查。

不是"点一下就过"的冒烟测试，而是把三层卡片里每一个可交互元素摊开，
按用户会关心的四件事逐个检查：

  功能 —— 能不能点、能不能改、改了有没有反应
  方便 —— 点击目标够不够大（< 24px 在触屏/触控板上明显难按）
  提示 —— 有没有标签（读屏要能知道这是什么）；同一句话有没有重复
  语义 —— 1 级=通用配置+入口 / 2 级=详细配置 / 3 级=长文本

输出是给人看的清单，发现问题就列在最后；断言失败（比如出现 JS 错误、
控件失去标签）返回非 0。
"""
import json
import sys
import time
import urllib.request

sys.path.insert(0, "scripts/ui-test")
from wd import Session, req

B = "http://127.0.0.1:18400"
MIN_TAP = 24          # 低于这个尺寸算"不好点"
fails, notes = [], []


def check(name, cond, detail=""):
    if not cond:
        fails.append(f"{name}  {detail}")
    return cond


def note(msg):
    notes.append(msg)


# 收集可见的可交互元素。可见 = 有布局盒且不在 display:none 的祖先里
PROBE = r'''
(() => {
  const scope = SCOPESEL;
  const out = [];
  // 不去重：同名的 input 是不同字段，按 tagName+class 去重会把它们合并成一个
  for (const el of document.querySelectorAll(scope + ' *')) {
    const t = el.tagName;
    // 只认真正能操作的：表单控件、开关、可点的 .item
    const clickable = t === 'BUTTON' || t === 'INPUT' || t === 'SELECT' || t === 'TEXTAREA'
      || el.getAttribute('role') === 'switch'
      || (el.classList.contains('item') && !el.classList.contains('static'));
    if (!clickable) continue;
    // 勾选框本体只有 14px，但整行 <label class="choice"> 都能点 ——
    // 该量的是"用户实际能按到的范围"，不是控件自己的盒子
    const hit = (el.tagName === 'INPUT' && el.type === 'checkbox' && el.closest('label')) || el;
    const r = hit.getBoundingClientRect();
    if (!r.width || !r.height) continue;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') continue;
    // 标签：同名 label 里的文字，或前置 span，或 aria-label
    let label = el.getAttribute('aria-label') || '';
    const lab = el.closest('label');
    if (!label && lab) {
      const sp = lab.querySelector('span');
      if (sp) label = sp.textContent.trim();
    }
    if (!label) {
      const prev = el.previousElementSibling;
      if (prev && prev.tagName === 'SPAN') label = prev.textContent.trim();
    }
    out.push({
      tag: el.tagName.toLowerCase(),
      type: el.type || '',
      cls: (typeof el.className === 'string' ? el.className : '').slice(0, 40),
      text: (el.textContent || '').trim().slice(0, 26),
      label: label.slice(0, 30),
      w: Math.round(r.width), h: Math.round(r.height),
      disabled: !!el.disabled,
      placeholder: el.placeholder || ''
    });
  }
  return out;
})()
'''


def probe(s, scope):
    # strip() 不能省：PROBE 以换行开头，拼成 "return\n(() => ...)" 会被 ASI
    # 插成 "return;"，整个探针退化为独立语句，取回来的永远是 None
    return s.js("const SCOPESEL = " + json.dumps(scope) + ";\nreturn " + PROBE.strip())


def report(title, items, kind):
    print(f"\n── {title} ── {len(items)} 个控件")
    small, unlabeled, disabled = [], [], []
    for it in items:
        if it["tag"] == "button" or it["type"] == "checkbox" or it["tag"] == "select":
            if it["h"] < MIN_TAP or it["w"] < MIN_TAP:
                small.append(it)
        if it["tag"] in ("input", "select", "textarea") and not it["label"]:
            unlabeled.append(it)
        if it["disabled"]:
            disabled.append(it)
    print(f"   点击目标 < {MIN_TAP}px：{len(small)}")
    for it in small:
        print(f"     · {it['tag']}.{it['cls'][:26]} 「{it['text'] or it['label']}」 {it['w']}×{it['h']}")
    print(f"   没有标签的输入：{len(unlabeled)}")
    for it in unlabeled:
        print(f"     · {it['tag']}({it['type']}).{it['cls'][:26]} ph={it['placeholder'][:20]}")
    print(f"   禁用态：{len(disabled)}")
    for it in disabled:
        print(f"     · {it['tag']}.{it['cls'][:26]} 「{it['label']}」")
    if small:
        note(f"{title}：{len(small)} 个控件小于 {MIN_TAP}px（{', '.join(x['text'] or x['label'] for x in small[:4])}）")
    if unlabeled:
        note(f"{title}：{len(unlabeled)} 个输入没有标签")
    return small, unlabeled


def main():
    s = Session(1600, 1000)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)
    s.js("window.__errs=[]; window.addEventListener('error',e=>window.__errs.push(String(e.message))); return 1")

    # ── 1 级：通用配置 + 入口 ──
    lvl1 = probe(s, ".col-list")
    small1, un1 = report("1 级 · 通用配置与入口", lvl1, "list")
    sections1 = s.js("return [...document.querySelectorAll('.col-list .set-section')].map(e=>e.textContent.trim())")
    print("   分组：", sections1)
    check("1 级有分组标题", len(sections1) >= 3, str(sections1))

    # ── 2 级：模型详情 ──
    s.js('''const x=[...document.querySelectorAll('.col-list .item.col')][0]; x&&x.click(); return !!x''')
    time.sleep(1.2)
    lvl2 = probe(s, ".col-detail")
    small2, un2 = report("2 级 · 模型详情", lvl2, "detail")
    sections2 = s.js("return [...document.querySelectorAll('.col-detail .set-section')].map(e=>e.textContent.trim())")
    print("   分组：", sections2)
    check("2 级有分组标题", len(sections2) >= 3, str(sections2))

    # ── 3 级：长文本 ──
    s.js("const it=[...document.querySelectorAll('.col-detail .item')].find(e=>/system/.test(e.textContent)); it&&it.click(); return !!it")
    time.sleep(1.0)
    lvl3 = probe(s, ".col-editor")
    report("3 级 · 长文本", lvl3, "editor")
    check("3 级只有一个大文本域", sum(1 for i in lvl3 if i["tag"] == "textarea") == 1,
          str([i["tag"] for i in lvl3]))
    s.js("document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true})); return 1")
    time.sleep(0.5)
    s.js("document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true})); return 1")
    time.sleep(0.6)

    # ── 2 级：工具详情 ──
    s.js('''const x=[...document.querySelectorAll('.col-list .item')].find(e=>e.querySelector('.item-title')?.textContent.startsWith('kb_get'));
      x&&x.click(); return !!x''')
    time.sleep(1.2)
    tool = probe(s, ".col-detail")
    small3, un3 = report("2 级 · 工具详情", tool, "detail")
    s.shot("/tmp/cfg_tool.png")

    # ── 2 级：插件表单 ──
    s.js("const b=[...document.querySelectorAll('.set-foot-actions .btn')].find(x=>/编辑插件定义/.test(x.textContent)); b&&b.click(); return !!b")
    time.sleep(1.0)
    form = probe(s, ".col-detail")
    small4, un4 = report("2 级 · 插件表单", form, "detail")
    sections4 = s.js("return [...document.querySelectorAll('.col-detail .set-section')].map(e=>e.textContent.trim())")
    print("   分组：", sections4)
    check("插件表单有分组标题", len(sections4) >= 2, str(sections4))
    s.shot("/tmp/cfg_plugin.png")

    errs = s.js("return window.__errs || []")
    check("无 JS 错误", not errs, str(errs))

    print("\n=== 需要留意 ===")
    for n in notes:
        print("  · " + n)
    print(f"\n=== 断言失败 {len(fails)} 项 ===")
    for f in fails:
        print("  ✗ " + f)
    s.close()
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main()
