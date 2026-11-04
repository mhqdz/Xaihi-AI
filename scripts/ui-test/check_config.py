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
        ("接口地址", "http://127.0.0.1:18320/180/v2"),
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


def level1(s):
    """一级卡：常规配置的开关与数字。

    「立即清理」故意不点 —— 它会真的删截图目录里的文件，这里没有东西可恢复。
    它的存在、尺寸、type 在盘点阶段已经查过。
    """
    print("\n" + "=" * 62)
    print("第三部分：一级卡常规配置")
    print("=" * 62)
    orig = api("GET", "/api/settings")[1]
    print(f"   原值 tool_rounds={orig.get('tool_rounds')} "
          f"auto_archive={orig.get('auto_archive_enabled')} days={orig.get('auto_archive_days')}")
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)

    got = s.js("""const i=[...document.querySelectorAll('.col-list input[type=number]')][0];
      if(!i) return 'NO_INPUT';
      const set=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;
      set.call(i,'37'); i.dispatchEvent(new Event('input',{bubbles:true})); return i.value;""")
    time.sleep(0.3)
    check("1 级数字可编辑", got == "37", got)
    check("1 级改动触发脏标记", dirty(s), "")

    st = s.js("""const b=[...document.querySelectorAll('.col-list [role=switch]')][1];
      if(!b) return 'NO_SWITCH';
      return {before:b.getAttribute('aria-checked'), label:b.getAttribute('aria-label')};""")
    s.js("document.querySelectorAll('.col-list [role=switch]')[1].click(); return 1")
    time.sleep(0.6)
    st['after'] = s.js("return document.querySelectorAll('.col-list [role=switch]')[1].getAttribute('aria-checked')")
    check("1 级开关可切换", isinstance(st, dict) and st.get("before") != st.get("after"), st)

    click_save(s)
    now = api("GET", "/api/settings")[1]
    check("保存后轮次上限生效", now.get("tool_rounds") == 37, now.get("tool_rounds"))
    check("保存后归档开关翻转", now.get("auto_archive_enabled") != orig.get("auto_archive_enabled"),
          f"{orig.get('auto_archive_enabled')} -> {now.get('auto_archive_enabled')}")

    # 运行中提示写的是 localStorage，不是后端
    r = s.js("""const t=document.querySelector('.ui-quips');
      if(!t) return 'NO_QUIPS';
      const set=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;
      set.call(t, ['测试一句','测试两句'].join(String.fromCharCode(10)));
      t.dispatchEvent(new Event('input',{bubbles:true}));
      return JSON.parse(localStorage.getItem('ui.quips')||'[]');""")
    time.sleep(0.3)
    check("运行中提示写进 localStorage", r == ["测试一句", "测试两句"], r)
    check("运行中提示不触发后端脏标记", not dirty(s), "它是本机偏好，不该进保存清单")

    api("PUT", "/api/settings", orig)
    restored = api("GET", "/api/settings")[1]
    check("设置已还原", restored.get("tool_rounds") == orig.get("tool_rounds")
          and restored.get("auto_archive_enabled") == orig.get("auto_archive_enabled"),
          f"{restored.get('tool_rounds')}/{restored.get('auto_archive_enabled')}")


def plugin_flow(s):
    """外置插件：新建 → 详情 → 编辑 → 保存 → 说明编辑 → 删除。"""
    print("\n" + "=" * 62)
    print("第四部分：插件表单闭环")
    print("=" * 62)
    st, _ = api("POST", "/api/plugins", {"name": "cfg_plugin", "description": "测试插件",
                                         "command": ["/bin/echo", "{{msg}}"], "timeoutMs": 5000,
                                         "params": {"msg": {"type": "string"}},
                                         "required": ["msg"]})
    check("探针插件建好", st == 200, st)
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)

    ok = s.js("""const x=[...document.querySelectorAll('.col-list .item')]
        .find(e=>e.querySelector('.item-title')?.textContent.startsWith('cfg_plugin'));
      x&&x.click(); return !!x;""")
    time.sleep(1.2)
    check("插件详情可打开", ok, "")
    info = s.js("""return [...document.querySelectorAll('.col-detail .tool-meta')]
        .map(e=>e.textContent.trim()).slice(0,4);""")
    print("   详情信息：", info)

    s.js("const b=[...document.querySelectorAll('.set-foot-actions .btn')].find(x=>/编辑插件定义/.test(x.textContent)); b&&b.click(); return !!b")
    time.sleep(1.0)
    check("编辑表单打开", s.js("return !!document.querySelector('.plugin-cmd')"), "")

    got = s.js("""const f=[...document.querySelectorAll('.col-detail .field')]
        .find(x=>(x.querySelector('span')||{}).textContent?.trim()==='超时（毫秒）');
      if(!f) return 'NO_FIELD';
      const el=f.querySelector('input');
      const set=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;
      set.call(el,'7000'); el.dispatchEvent(new Event('input',{bubbles:true})); return el.value;""")
    time.sleep(0.3)
    check("插件超时可改", got == "7000", got)

    sw_before = s.js("return document.querySelector('.col-detail .switch-row [role=switch]').getAttribute('aria-checked')")
    # 点整行的空白处（不是那个 38×24 的钮）—— 验证"整行可点"真的成立
    s.js("document.querySelector('.col-detail .switch-row').click(); return 1")
    time.sleep(0.6)   # 等 React 重渲染
    sw_after = s.js("return document.querySelector('.col-detail .switch-row [role=switch]').getAttribute('aria-checked')")
    check("常驻进程开关可切换（整行可点）", sw_before != sw_after, f"{sw_before} -> {sw_after}")
    # 再点回来：/bin/echo 撑不起 JSON-RPC 常驻进程，开着保存会让插件加载失败、
    # 从工具列表里静默消失（见 AUDIT 里记的那条）。这里只验证开关本身。
    s.js("document.querySelector('.col-detail .switch-row').click(); return 1")
    time.sleep(0.6)
    check("开关可切回", s.js("return document.querySelector('.col-detail .switch-row [role=switch]').getAttribute('aria-checked')") == sw_before, "")

    s.js("const b=[...document.querySelectorAll('.col-detail .title-actions .btn')].find(x=>/保存/.test(x.textContent)); b&&b.click(); return !!b")
    time.sleep(1.8)
    check("保存后回到详情", s.js("return !!document.querySelector('.set-foot-actions')"), "")
    st, lst = api("GET", "/api/tools")
    t = next((x for x in (lst or []) if x["name"] == "cfg_plugin"), {})
    check("插件超时已写入", t.get("timeout_ms") == 7000, t.get("timeout_ms"))
    check("插件常驻标记已写入", t.get("persistent") is False, t.get("persistent"))

    # 说明（3 级）
    s.js("const it=[...document.querySelectorAll('.col-detail .item')][0]; it&&it.click(); return !!it")
    time.sleep(1.0)
    s.js("""const t=document.querySelector('.col-editor textarea');
      const set=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;
      set.call(t,'改过的测试插件说明'); t.dispatchEvent(new Event('input',{bubbles:true})); return 1;""")
    time.sleep(0.3)
    click_save(s)
    st, lst = api("GET", "/api/tools")
    t = next((x for x in (lst or []) if x["name"] == "cfg_plugin"), {})
    check("工具说明已保存", t.get("description") == "改过的测试插件说明", t.get("description"))

    api("DELETE", "/api/plugins/cfg_plugin")
    st, lst = api("GET", "/api/tools")
    check("探针插件已删除", not any(x["name"] == "cfg_plugin" for x in (lst or [])), "")


def tool_issue_notice(s):
    """插件加载失败时，界面要能说出原因，而不是让它凭空消失。"""
    print("\n" + "=" * 62)
    print("第五部分：插件加载失败的提示")
    print("=" * 62)
    import os
    path = os.path.expanduser("~/.data/agentd/tools.d/zz_probe_bad.json")
    with open(path, "w", encoding="utf-8") as f:
        json.dump({"name": "zz_probe_bad", "description": "故意坏的常驻插件",
                   "command": ["/bin/echo", "x"], "persistent": True, "timeoutMs": 3000}, f)
    try:
        s.get(B + "/?settings=1&conv=51")
        time.sleep(2.5)
        r = s.js("""const el=document.querySelector('.tool-issue');
          if(!el) return null;
          const b=el.getBoundingClientRect();
          return {text:el.textContent.trim(), w:Math.round(b.width), h:Math.round(b.height)};""")
        check("加载失败有可见提示", isinstance(r, dict) and r.get("h", 0) > 0, r)
        check("提示里带上了文件名与原因",
              isinstance(r, dict) and "zz_probe_bad.json" in r.get("text", "")
              and "常驻插件启动失败" in r.get("text", ""), (r or {}).get("text", "")[:60])
        st, d = api("GET", "/api/tools/issues")
        check("issues 端点也报了", st == 200 and len(d.get("issues") or []) >= 1, d)
        check("坏插件确实不在列表里",
              not any(x["name"] == "zz_probe_bad" for x in (api("GET", "/api/tools")[1] or [])), "")
    finally:
        os.remove(path)
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.0)
    check("删掉坏插件后提示消失", s.js("return !document.querySelector('.tool-issue')"), "")


def tasks_section(s):
    """一级卡的任务区：空状态 → 造一条 → 详情 → 清理。"""
    print("\n" + "=" * 62)
    print("第六部分：任务区")
    print("=" * 62)
    import os
    import sqlite3
    import dbtool

    db = os.path.expanduser("~/.data/agentd/agentd.db")
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)
    empty = s.js("""const el=[...document.querySelectorAll('.col-list .set-meta')]
        .find(e=>/还没有任务|可用配方|未注册配方/.test(e.textContent));
      return el ? el.textContent.trim() : null;""")
    check("空状态有说明", empty is not None and ("还没有任务" in empty), empty)

    cid = dbtool.new_conv("cfg-task")
    tid, child = None, None
    try:
        st, d = api("POST", "/api/tasks", {"kind": "smoke", "conv_id": cid, "card": {"goal": "cfg"}})
        tid = d.get("task_id")
        check("任务创建成功", st == 200 and tid, f"{st} {d}")
        # smoke 配方几步、要几秒，等它跑完
        for _ in range(30):
            time.sleep(0.5)
            st, det = api("GET", f"/api/tasks/{tid}")
            t = (det or {}).get("task") or {}
            if t.get("status") in ("done", "failed", "interrupted"):
                child = t.get("child_conv")
                break
        # smoke 配方不建子会话，child_conv 是 0 —— 判据应该是"进了终态"
        check("任务跑完", t.get("status") in ("done", "failed", "interrupted"), t.get("status"))

        s.get(B + "/?settings=1&conv=51")
        time.sleep(2.5)
        row = s.js("""const el=[...document.querySelectorAll('.col-list .item.col')]
            .find(e=>/^#%d\\b/.test(e.querySelector('.item-title')?.textContent.trim()||''));
          if(!el) return null;
          const b=el.getBoundingClientRect();
          return {text:el.textContent.trim().slice(0,60), h:Math.round(b.height)};""" % tid)
        check("任务出现在列表里", isinstance(row, dict) and row.get("h", 0) >= 24, row)
        check("任务行有状态标签", isinstance(row, dict) and "done" in row.get("text", ""), (row or {}).get("text", "")[:40])

        s.js("""const el=[...document.querySelectorAll('.col-list .item.col')]
            .find(e=>/^#%d\\b/.test(e.querySelector('.item-title')?.textContent.trim()||''));
          el&&el.click(); return !!el;""" % tid)
        time.sleep(1.2)
        detail = s.js("""const steps=[...document.querySelectorAll('.task-step')];
          return {n: steps.length, first: steps[0]?.textContent.trim().slice(0,40)||null};""")
        check("点开能看到步骤", isinstance(detail, dict) and detail.get("n", 0) > 0, detail)

        if child:
            btn = s.js("""const b=[...document.querySelectorAll('button')].find(x=>/打开子会话/.test(x.textContent));
              return !!b;""")
            check("有打开子会话的入口", btn, "")
    finally:
        con = sqlite3.connect(db)
        if tid:
            # child_conv 对 smoke 配方是 0，但任务回报仍会落进 outbox；
            # 按文本兜底，避免留下指向不存在会话的孤儿行
            con.execute("delete from outbox where text like ?", ("%%smoke #%d ·%%" % tid,))
            con.execute("delete from outbox where conv_id in (?, ?)", (child or -1, cid))
            con.execute("delete from tasks where id = ?", (tid,))
        con.commit()
        con.close()
        if child:
            dbtool.drop_conv(child)
        dbtool.drop_conv(cid)
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.0)
    check("清理后回到空状态", s.js("""return [...document.querySelectorAll('.col-list .set-meta')]
        .some(e=>/还没有任务/.test(e.textContent));"""), "")


def main():
    s = Session(1600, 1000)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
    try:
        inventory(s)
        functional(s)
        level1(s)
        plugin_flow(s)
        tool_issue_notice(s)
        tasks_section(s)
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
