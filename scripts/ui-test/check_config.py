#!/usr/bin/env python3
"""配置面板逐控件走查 + 功能测试。

上半部分（盘点）：把三层卡片里每一个可交互元素摊开，按用户会关心的四件事
逐个检查 —— 功能（能不能点/能不能改）、方便（点击目标够不够大）、
提示（有没有标签、同一句话有没有重复）、语义（1 级通用配置 / 2 级详细配置 /
3 级长文本）。

下半部分（功能）：真的改每一个输入，确认状态跟上了、保存真的落到了后端、
取消真的回滚。用一个临时 profile 做靶子，跑完删掉。
"""
import json
import sys
import time
import urllib.request
import urllib.error

sys.path.insert(0, "scripts/ui-test")
from wd import Session, req

B = "http://127.0.0.1:18400"
MIN_TAP = 24
fails, notes = [], []
PROBE_NAME = "cfg-probe"


def check(name, cond, detail=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (("  " + str(detail)) if detail else ""))
    if not cond:
        fails.append(f"{name}  {detail}")
    return cond


def api(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(B + path, data=data, method=method)
    if data is not None:
        r.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(r, timeout=15) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw or b"{}")
        except Exception:
            return e.code, {"raw": raw[:80].decode("utf-8", "replace")}
    except Exception as e:
        return -1, {"err": str(e)[:80]}


def get_profile(name):
    st, lst = api("GET", "/api/profiles")
    for p in (lst or []):
        if p["name"] == name:
            return p
    return None


# ── 页面内探针 ──
PROBE = r'''
(() => {
  const scope = SCOPESEL;
  const out = [];
  // 不去重：同名的 input 是不同字段
  for (const el of document.querySelectorAll(scope + ' *')) {
    const t = el.tagName;
    const clickable = t === 'BUTTON' || t === 'INPUT' || t === 'SELECT' || t === 'TEXTAREA'
      || el.getAttribute('role') === 'switch'
      || (el.classList.contains('item') && !el.classList.contains('static'));
    if (!clickable) continue;
    // 勾选框本体 14px，但整行 label 都能点 —— 量"实际能按到的范围"
    const hit = (el.tagName === 'INPUT' && el.type === 'checkbox' && el.closest('label')) || el;
    const r = hit.getBoundingClientRect();
    if (!r.width || !r.height) continue;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') continue;
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
      tag: el.tagName.toLowerCase(), type: el.type || '',
      cls: (typeof el.className === 'string' ? el.className : '').slice(0, 40),
      text: (el.textContent || '').trim().slice(0, 26),
      label: label.slice(0, 30),
      w: Math.round(r.width), h: Math.round(r.height),
      disabled: !!el.disabled
    });
  }
  return out;
})()
'''

SET_FIELD = r'''
const label = arguments[0], value = arguments[1];
const f = [...document.querySelectorAll('.col-detail .field')]
  .find(x => (x.querySelector('span') || {}).textContent?.trim() === label);
if (!f) return 'NO_FIELD';
const el = f.querySelector('input,select,textarea');
if (!el) return 'NO_CTL';
const proto = el.tagName === 'SELECT' ? HTMLSelectElement.prototype
  : el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, String(value));
el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }));
return el.value;
'''


def probe(s, scope):
    # strip() 不能省：PROBE 以换行开头，拼成 "return\n(() => ...)" 会被 ASI
    # 插成 "return;"，探针退化为独立语句，取回来永远是 None
    return s.js("const SCOPESEL = " + json.dumps(scope) + ";\nreturn " + PROBE.strip())


def report(title, items):
    print(f"\n── {title} ── {len(items)} 个控件")
    small, unlabeled = [], []
    for it in items:
        if it["tag"] in ("button", "select") or it["type"] == "checkbox":
            if it["h"] < MIN_TAP or it["w"] < MIN_TAP:
                small.append(it)
        if it["tag"] in ("input", "select", "textarea") and not it["label"]:
            unlabeled.append(it)
    print(f"   < {MIN_TAP}px：{len(small)}   无标签输入：{len(unlabeled)}   "
          f"禁用：{sum(1 for i in items if i['disabled'])}")
    for it in small:
        print(f"     · 小目标 {it['tag']}.{it['cls'][:24]} 「{it['text'] or it['label']}」 {it['w']}×{it['h']}")
    for it in unlabeled:
        print(f"     · 无标签 {it['tag']}({it['type']}).{it['cls'][:24]}")
    if small:
        note(f"{title}：{len(small)} 个控件 < {MIN_TAP}px")
    if unlabeled:
        note(f"{title}：{len(unlabeled)} 个输入没有标签")
    return small, unlabeled


def dirty(s):
    return s.js("return !!document.querySelector('.title-actions')")


def click_save(s):
    s.js("const b=document.querySelector('.col-list .title-actions .btn.save'); b&&b.click(); return !!b")
    time.sleep(0.9)
    s.js("const b=[...document.querySelectorAll('.sheet-ft .btn')].find(x=>/确认保存/.test(x.textContent)); b&&b.click(); return !!b")
    time.sleep(2.0)


def click_cancel(s):
    s.js("const b=document.querySelector('.col-list .title-actions .btn'); b&&b.click(); return !!b")
    time.sleep(0.7)


def open_profile(s, name):
    ok = s.js("""const x=[...document.querySelectorAll('.col-list .item.col')].find(e=>/%s/.test(e.textContent));
      x&&x.click(); return !!x""" % name)
    time.sleep(1.2)
    return ok


def inventory(s):
    print("=" * 62)
    print("第一部分：控件盘点")
    print("=" * 62)
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)
    s.js("window.__errs=[]; window.addEventListener('error',e=>window.__errs.push(String(e.message))); return 1")

    report("1 级 · 通用配置与入口", probe(s, ".col-list"))
    sec1 = s.js("return [...document.querySelectorAll('.col-list .set-section')].map(e=>e.textContent.trim())")
    print("   分组：", sec1)
    check("1 级分组齐全", len(sec1) >= 3, sec1)

    open_profile(s, "180-low")
    report("2 级 · 模型详情", probe(s, ".col-detail"))
    sec2 = s.js("return [...document.querySelectorAll('.col-detail .set-section')].map(e=>e.textContent.trim())")
    print("   分组：", sec2)
    check("2 级分组齐全", len(sec2) >= 3, sec2)

    s.js("const it=[...document.querySelectorAll('.col-detail .item')].find(e=>/system/.test(e.textContent)); it&&it.click(); return !!it")
    time.sleep(1.0)
    lvl3 = probe(s, ".col-editor")
    report("3 级 · 长文本", lvl3)
    check("3 级只有一个大文本域", sum(1 for i in lvl3 if i["tag"] == "textarea") == 1,
          [i["tag"] for i in lvl3])
    for _ in range(2):
        s.js("document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true})); return 1")
        time.sleep(0.5)

    s.js("""const x=[...document.querySelectorAll('.col-list .item')].find(e=>e.querySelector('.item-title')?.textContent.startsWith('kb_get'));
      x&&x.click(); return !!x""")
    time.sleep(1.2)
    report("2 级 · 工具详情", probe(s, ".col-detail"))

    s.js("const b=[...document.querySelectorAll('.set-foot-actions .btn')].find(x=>/编辑插件定义/.test(x.textContent)); b&&b.click(); return !!b")
    time.sleep(1.0)
    report("2 级 · 插件表单", probe(s, ".col-detail"))
    sec4 = s.js("return [...document.querySelectorAll('.col-detail .set-section')].map(e=>e.textContent.trim())")
    print("   分组：", sec4)
    check("插件表单分组齐全", len(sec4) >= 2, sec4)

    errs = s.js("return window.__errs || []")
    check("盘点过程无 JS 错误", not errs, errs)


def functional(s):
    print("\n" + "=" * 62)
    print("第二部分：逐字段功能")
    print("=" * 62)
    api("DELETE", "/api/profiles/" + PROBE_NAME)
    api("POST", "/api/profiles", {"name": PROBE_NAME, "base_url": "http://127.0.0.1:18320/180/v1",
                                  "model": "probe-model", "system": "probe-system",
                                  "reasoning_effort": "high", "context_window": 0,
                                  "max_prompt_tokens": 0, "compact_pct": 0, "tools": []})
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)
    check("探针 profile 打开", open_profile(s, PROBE_NAME), "")

    cases = [
        ("接口地址 (url+/v1)", "http://127.0.0.1:18320/180/v2"),
        ("API Key", "sk-probe-123"),
        ("窗口大小 (tokens)", "4096"),
        ("输入上限 (tokens)", "3000"),
        ("压缩阈值 (%)", "70"),
    ]
    for label, val in cases:
        got = s.js(SET_FIELD, label, val)
        time.sleep(0.25)
        check(f"字段「{label}」可编辑", str(got) == val, f"控件值={got}")
        check(f"字段「{label}」触发脏标记", dirty(s), "")

    got = s.js(SET_FIELD, "服务商", "openai"); time.sleep(0.25)
    check("下拉「服务商」可选", got == "openai", got)
    got = s.js(SET_FIELD, "思考强度", "low"); time.sleep(0.25)
    check("下拉「思考强度」可选", got == "low", got)

    r = s.js("""const c=[...document.querySelectorAll('.col-detail .choice')];
      const box=[...c].find(e=>e.textContent.trim()==='bash');
      if(!box) return 'NO_CHOICE';
      box.querySelector('input').click();
      return {on: box.classList.contains('on'), checked: box.querySelector('input').checked};""")
    time.sleep(0.3)
    check("工具勾选可切换", isinstance(r, dict) and r.get("checked"), r)

    s.js("const it=[...document.querySelectorAll('.col-detail .item')].find(e=>/system/.test(e.textContent)); it&&it.click(); return !!it")
    time.sleep(1.0)
    r = s.js("""const t=document.querySelector('.col-editor textarea');
      if(!t) return 'NO_EDITOR';
      const set=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;
      set.call(t,'probe-system-v2'); t.dispatchEvent(new Event('input',{bubbles:true}));
      return t.value;""")
    time.sleep(0.3)
    check("system 可编辑（3 级）", r == "probe-system-v2", r)
    s.js("document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true})); return 1")
    time.sleep(0.5)

    click_save(s)
    p = get_profile(PROBE_NAME) or {}
    check("保存后 base_url 生效", p.get("base_url") == "http://127.0.0.1:18320/180/v2", p.get("base_url"))
    check("保存后 api_key 生效", p.get("api_key") == "sk-probe-123", p.get("api_key"))
    check("保存后 context_window 生效", p.get("context_window") == 4096, p.get("context_window"))
    check("保存后 max_prompt_tokens 生效", p.get("max_prompt_tokens") == 3000, p.get("max_prompt_tokens"))
    check("保存后 compact_pct 生效", p.get("compact_pct") == 70, p.get("compact_pct"))
    check("保存后 provider 生效", p.get("provider") == "openai", p.get("provider"))
    check("保存后 reasoning_effort 生效", p.get("reasoning_effort") == "low", p.get("reasoning_effort"))
    check("保存后 system 生效", p.get("system") == "probe-system-v2", p.get("system"))
    check("保存后 tools 生效", "bash" in (p.get("tools") or []), p.get("tools"))
    check("保存后脏标记清空", not dirty(s), "")

    open_profile(s, PROBE_NAME)
    s.js(SET_FIELD, "压缩阈值 (%)", "55")
    time.sleep(0.25)
    check("改后出现脏标记", dirty(s), "")
    click_cancel(s)
    p2 = get_profile(PROBE_NAME) or {}
    check("取消后服务端未变", p2.get("compact_pct") == 70, p2.get("compact_pct"))
    check("取消后脏标记清空", not dirty(s), "")

    api("DELETE", "/api/profiles/" + PROBE_NAME)
    check("探针已清理", get_profile(PROBE_NAME) is None, "")


def main():
    s = Session(1600, 1000)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
    try:
        inventory(s)
        functional(s)
    finally:
        api("DELETE", "/api/profiles/" + PROBE_NAME)
        s.close()
    print("\n=== 需要留意 ===")
    for n in notes:
        print("  · " + n)
    print(f"\n=== 断言失败 {len(fails)} 项 ===")
    for f in fails:
        print("  ✗ " + f)
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main()
