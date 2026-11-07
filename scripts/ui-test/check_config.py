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
    # 三层：探针 profile 引用一个已登记的模型（模型自己知道接口和上游 id）
    st, mdl = api("GET", "/api/models")
    models = mdl or []
    m0 = models[0]["name"] if models else ""
    api("POST", "/api/profiles", {"name": PROBE_NAME, "model": m0,
                                  "system": "probe-system",
                                  "reasoning_effort": "high",
                                  "max_prompt_tokens": 0, "compact_pct": 0, "tools": []})
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)
    check("探针 profile 打开", open_profile(s, PROBE_NAME), "")

    cases = [
        ("输入上限 (tokens)", "3000"),
        ("压缩阈值 (%)", "70"),
    ]
    for label, val in cases:
        got = s.js(SET_FIELD, label, val)
        time.sleep(0.25)
        check(f"字段「{label}」可编辑", str(got) == val, f"控件值={got}")
        check(f"字段「{label}」触发脏标记", dirty(s), "")

    # 模型下拉：换成另一个已登记的模型
    other = next((m["name"] for m in models if m["name"] != m0), "")
    got = s.js(SET_FIELD, "模型", other); time.sleep(0.25)
    check("下拉「模型」可选", got == other and other != "", got)
    got = s.js(SET_FIELD, "默认思考强度", "medium"); time.sleep(0.25)
    check("下拉「默认思考强度」可选", got == "medium", got)

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
    check("保存后模型引用生效", p.get("model") == other, p.get("model"))
    check("配置里不再存上游模型名（归模型层）", p.get("model") != "probe-model", p.get("model"))
    check("配置里不再存窗口（归模型层）", not p.get("context_window"), p.get("context_window"))
    check("保存后 max_prompt_tokens 生效", p.get("max_prompt_tokens") == 3000, p.get("max_prompt_tokens"))
    check("保存后 compact_pct 生效", p.get("compact_pct") == 70, p.get("compact_pct"))
    check("保存后默认思考强度生效", p.get("reasoning_effort") == "medium", p.get("reasoning_effort"))
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
    """外置插件：新建 → 详情 → 编辑 → 保存 → 改名 → 说明编辑 → 删除。"""
    import glob
    import os
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

    click_save(s)   # 插件表单没有自己的保存按钮，走一级卡的统一保存
    # 没有"详情态"了：保存后仍停在表单里，只是变成未修改状态
    check("保存后仍停在工具表单", s.js("return !!document.querySelector('.col-detail .plugin-cmd')"), "")
    check("保存后名称是新的", s.js("const i=document.querySelector('.col-detail input'); return i? i.value : ''") == "cfg_plugin", "")
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

    # 改名：内容里的 name 换了，文件名不动，profile 里的引用要跟着换
    # （三层之后 profile 必须引用一个真模型，不能再用裸 model 名）
    st, _mdl = api("GET", "/api/models")
    _m0 = (_mdl or [{}])[0].get("name", "")
    st, _ = api("POST", "/api/profiles", {"name": "rename_probe", "model": _m0, "tools": ["cfg_plugin"]})
    check("探针 profile 引用插件", st == 200, st)
    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)
    s.js("window.__alerts2=[]; window.alert=(m)=>{window.__alerts2.push(String(m))}; return 1")
    s.js("""const x=[...document.querySelectorAll('.col-list .item')]
        .find(e=>e.querySelector('.item-title')?.textContent.startsWith('cfg_plugin'));
      x&&x.click(); return !!x;""")
    time.sleep(1.0)
    s.js("const b=[...document.querySelectorAll('.set-foot-actions .btn')].find(x=>/编辑插件定义/.test(x.textContent)); b&&b.click(); return !!b")
    time.sleep(1.0)
    got = s.js("""const f=[...document.querySelectorAll('.col-detail .field')]
        .find(x=>(x.querySelector('span')||{}).textContent?.trim()==='名称');
      const el=f.querySelector('input');
      if(el.disabled) return 'DISABLED';
      const set=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;
      set.call(el,'cfg_plugin2'); el.dispatchEvent(new Event('input',{bubbles:true}));
      return el.value;""")
    check("名称可以改（不再禁用）", got == "cfg_plugin2", got)
    click_save(s)   # 插件表单没有自己的保存按钮，走一级卡的统一保存
    names = [x["name"] for x in (api("GET", "/api/tools")[1] or [])]
    check("改名后新名字在列表里", "cfg_plugin2" in names, [n for n in names if n.startswith("cfg")])
    check("旧名字不在了", "cfg_plugin" not in names, "")
    st, lst = api("GET", "/api/profiles")
    tools_of = next((p["tools"] for p in (lst or []) if p["name"] == "rename_probe"), [])
    check("profile 里的引用跟着换了", "cfg_plugin2" in tools_of, tools_of)
    # 文件名必须跟着改：只改内容、文件名不动的话，新建一个与旧文件名同名的
    # 插件会把它覆盖掉（工具凭空消失）
    files = sorted(os.path.basename(p) for p in glob.glob(os.path.join(
        os.path.expanduser("~/.data/agentd/tools.d"), "cfg_plugin*.json")))
    check("文件名跟着改成新名字", files == ["cfg_plugin2.json"], files)
    # 撞名要拒绝，而不是覆盖。必须撞一个**确实存在**的插件：
    # 改成空闲名字是合法操作（那是普通改名），不是撞名。
    api("POST", "/api/plugins", {"name": "cfg_neighbor", "description": "邻居",
                                 "command": ["/bin/echo", "n"]})
    st, d2 = api("POST", "/api/plugins", {"name": "cfg_neighbor", "originalName": "cfg_plugin2",
                                          "oldName": "cfg_plugin2", "description": "抢占",
                                          "command": ["/bin/echo", "y"]})
    check("改名撞到已存在的名字被拒绝", st == 400, f"{st} {d2.get('error')}")
    check("被拒绝后原插件还在",
          any(x["name"] == "cfg_plugin2" for x in (api("GET", "/api/tools")[1] or [])), "")
    api("DELETE", "/api/plugins/cfg_neighbor")
    api("DELETE", "/api/profiles/rename_probe")
    api("DELETE", "/api/plugins/cfg_plugin2")
    st, lst = api("GET", "/api/tools")
    check("探针插件已删除", not any(x["name"].startswith("cfg_plugin") for x in (lst or [])), "")


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


def narrow_flow():
    """窄屏（900px）：卡片层叠、返回箭头、删除按钮换位、底部操作条。

    1600 宽下这些分支根本不会执行 —— 窄屏只显示最上面那张卡，
    删除从标题栏挪到底部，保存/取消也从一级卡挪到底部固定条。
    """
    print("\n" + "=" * 62)
    print("第七部分：窄屏层叠")
    print("=" * 62)
    s = Session(900, 1000)
    try:
        req("POST", f"/session/{s.id}/window/rect", {"width": 900, "height": 1000})
        s.get(B + "/?settings=1&conv=51")
        time.sleep(2.5)

        def visible():
            return s.js("""return [...document.querySelectorAll('.set-cols .card')]
                .filter(c=>getComputedStyle(c).display!=='none').map(c=>c.className.trim());""")

        v1 = visible()
        check("1 级只显示一张卡", len(v1) == 1 and "col-list" in v1[0], v1)
        check("1 级有返回箭头", s.js("""const b=document.querySelector('.col-list .back');
            return !!b && getComputedStyle(b).display!=='none';"""), "")

        open_profile(s, "180-low")
        v2 = visible()
        check("2 级只显示详情卡", len(v2) == 1 and "col-detail" in v2[0], v2)
        dele = s.js("""const t=document.querySelector('.col-detail .del-top');
          const b=document.querySelector('.col-detail .del-bottom');
          const wrap=document.querySelector('.col-detail .del-bottom-wrap');
          const inf=el=>el?{d:getComputedStyle(el).display,h:Math.round(el.getBoundingClientRect().height)}:null;
          return {top:inf(t), bottom:inf(b), wrap:inf(wrap)};""")
        check("窄屏删除按钮在底部", isinstance(dele, dict)
              and dele.get("top", {}).get("d") == "none"
              and dele.get("bottom", {}).get("h", 0) >= 24, dele)

        s.js("const it=[...document.querySelectorAll('.col-detail .item')].find(e=>/system/.test(e.textContent)); it&&it.click(); return !!it")
        time.sleep(1.0)
        v3 = visible()
        check("3 级只显示编辑卡", len(v3) == 1 and "col-editor" in v3[0], v3)
        foot = s.js("""const f=document.querySelector('.set-col-foot');
          return f? f.textContent.trim() : null;""")
        check("3 级有字数统计", isinstance(foot, str) and "字" in foot, foot)

        s.js("const b=document.querySelector('.col-editor .back'); b&&b.click(); return !!b")
        time.sleep(0.8)
        check("返回后回到 2 级", "col-detail" in (visible() or [""])[0], visible())
        s.js("const b=document.querySelector('.col-detail .back'); b&&b.click(); return !!b")
        time.sleep(0.8)
        check("再返回回到 1 级", "col-list" in (visible() or [""])[0], visible())

        # 底部操作条：不脏时不存在，脏了才出现
        check("不脏时没有底部操作条", not s.js("return !!document.querySelector('.set-actions')"), "")
        open_profile(s, "180-low")
        s.js(SET_FIELD, "压缩阈值 (%)", "66")
        time.sleep(0.5)
        bar = s.js("""const b=document.querySelector('.set-actions');
          if(!b) return null;
          return {h:Math.round(b.getBoundingClientRect().height),
                  btns:[...b.querySelectorAll('.btn')].map(x=>x.textContent.trim())};""")
        check("脏了出现底部操作条", isinstance(bar, dict) and bar["h"] >= 24, bar)
        check("操作条有取消和保存", isinstance(bar, dict) and bar["btns"] == ["取消", "保存"], (bar or {}).get("btns"))

        # Esc 在窄屏下退出详情卡（改动转成草稿，不丢），面板不关
        s.js("document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true})); return 1")
        time.sleep(0.5)
        check("Esc 不关面板", s.js("return !!document.querySelector('.set-cols')"), "")
        check("Esc 退回列表且草稿仍在", s.js("""const cards=[...document.querySelectorAll('.set-cols .card')]
            .filter(c=>getComputedStyle(c).display!=='none').map(c=>c.className.trim());
          return cards.length===1 && cards[0].includes('col-list')
            && !!document.querySelector('.set-actions');"""), "")

        s.js("const b=document.querySelector('.set-actions .btn'); b&&b.click(); return !!b")
        time.sleep(0.7)
        check("取消后操作条消失", not s.js("return !!document.querySelector('.set-actions')"), "")
    finally:
        s.close()


def delete_flow():
    """模型删除：宽屏在标题栏，窄屏在卡片底部。都用探针 profile。"""
    print("\n" + "=" * 62)
    print("第八部分：删除模型的两种位置")
    print("=" * 62)
    for width, where, sel in ((1600, "标题栏", ".del-top"), (900, "卡片底部", ".del-bottom")):
        api("DELETE", "/api/profiles/del_probe")
        st, _mdl = api("GET", "/api/models")
        st, _ = api("POST", "/api/profiles", {"name": "del_probe",
                                              "model": (_mdl or [{}])[0].get("name", ""), "system": "x"})
        check(f"探针 profile 建出来了（{where}）", st == 200, st)
        s = Session(width, 1000)
        try:
            req("POST", f"/session/{s.id}/window/rect", {"width": width, "height": 1000})
            s.get(B + "/?settings=1&conv=51")
            time.sleep(2.5)
            s.js("window.__confirm=0; window.confirm=(m)=>{window.__confirm++; return true}; return 1")
            ok = open_profile(s, "del_probe")
            check(f"{where}：探针详情打开", ok, "")
            btn = s.js("""const b=document.querySelector('.col-detail %s');
              if(!b) return null;
              const r=b.getBoundingClientRect();
              const cs=getComputedStyle(b);
              return {h:Math.round(r.height), visible: cs.display!=='none' && r.height>0, text:b.textContent.trim()};""" % sel)
            check(f"{where}：删除按钮可见且够大", isinstance(btn, dict) and btn.get("visible")
                  and btn.get("h", 0) >= 24, btn)
            s.js("const b=document.querySelector('.col-detail %s'); b&&b.click(); return !!b" % sel)
            time.sleep(1.5)
            check(f"{where}：点完 confirm 一次", s.js("return window.__confirm") == 1, s.js("return window.__confirm"))
            check(f"{where}：profile 已删除（服务端）", get_profile("del_probe") is None, "")
            check(f"{where}：详情卡已关闭", not s.js("return !!document.querySelector('.col-detail')"), "")
        finally:
            s.close()
            api("DELETE", "/api/profiles/del_probe")


def deep_links():
    """深链：?profile= / &field= / ?tool= / &tooledit= / ?newplugin= / ?task="""
    print("\n" + "=" * 62)
    print("第九部分：深链")
    print("=" * 62)
    s = Session(1600, 1000)
    try:
        req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})

        def go(q):
            s.get(B + "/?settings=1&" + q)
            time.sleep(2.2)

        go("profile=180-low")
        check("?profile= 打开模型详情", s.js("""const t=document.querySelector('.col-detail .ef-label-t');
            return !!t && /180-low/.test(t.textContent);"""), "")

        go("profile=180-low&field=system")
        check("&field= 直接打开 3 级", s.js("return !!document.querySelector('.col-editor textarea')"), "")
        check("&field= 打开时不是脏的", not s.js("return !!document.querySelector('.title-actions')"), "")

        go("tool=kb_get")
        check("?tool= 打开工具详情", s.js("""const t=document.querySelector('.col-detail .ef-label-t');
            return !!t && /kb_get/.test(t.textContent);"""), "")
        check("?tool= 回显命令", s.js("""const t=document.querySelector('.col-detail .plugin-cmd');
          return !!t && t.value.includes('kb_get');"""), "")

        go("tool=kb_get&tooledit=1")
        check("&tooledit= 打开工具说明编辑", s.js("""const t=document.querySelector('.col-editor .ef-label-t');
            return !!t && /kb_get/.test(t.textContent);"""), "")

        go("newplugin=1")
        check("?newplugin= 打开新建插件表单", s.js("return !!document.querySelector('.plugin-cmd')"), "")
        check("新建时名称可编辑", s.js("const i=document.querySelector('.col-detail input'); return !!i && !i.disabled"), "")

        # 深链不应该把面板弄脏。注意只看一级卡：插件表单的标题栏本来就有
        # 它自己的取消/保存按钮（那不是"未保存"标记，是表单的正常动作）
        check("深链打开后一级卡没有未保存标记",
              not s.js("return !!document.querySelector('.col-list .title-actions')"), "")
    finally:
        s.close()


def task_deeplink():
    """?task=<id> 直接打开任务详情并滚到步骤。"""
    print("\n" + "=" * 62)
    print("第十部分：任务深链")
    print("=" * 62)
    import os
    import sqlite3
    import dbtool

    db = os.path.expanduser("~/.data/agentd/agentd.db")
    cid = dbtool.new_conv("cfg-task-dl")
    tid = None
    s = Session(1600, 1000)
    try:
        req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
        st, d = api("POST", "/api/tasks", {"kind": "smoke", "conv_id": cid, "card": {"goal": "dl"}})
        tid = d.get("task_id")
        check("任务建好", st == 200 and tid, f"{st} {d}")
        for _ in range(30):
            time.sleep(0.5)
            st, det = api("GET", f"/api/tasks/{tid}")
            if ((det or {}).get("task") or {}).get("status") in ("done", "failed", "interrupted"):
                break
        s.get(B + f"/?settings=1&task={tid}")
        time.sleep(2.8)
        check("?task= 打开了任务详情", s.js("return document.querySelectorAll('.task-step').length > 0"),
              s.js("return document.querySelectorAll('.task-step').length"))
        check("深链不把面板弄脏",
              not s.js("return !!document.querySelector('.col-list .title-actions')"), "")
    finally:
        s.close()
        con = sqlite3.connect(db)
        if tid:
            con.execute("delete from outbox where text like ?", ("%%smoke #%d ·%%" % tid,))
            con.execute("delete from outbox where conv_id = ?", (cid,))
            con.execute("delete from tasks where id = ?", (tid,))
        con.commit()
        con.close()
        dbtool.drop_conv(cid)


def new_plugin_ui():
    """从界面新建一个插件并保存（之前只用 API 建过探针）。"""
    print("\n" + "=" * 62)
    print("第十一部分：界面新建插件")
    print("=" * 62)
    s = Session(1600, 1000)
    try:
        req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
        s.get(B + "/?settings=1&newplugin=1")
        time.sleep(2.5)
        s.js("window.__alerts=[]; window.alert=(m)=>{window.__alerts.push(String(m))}; return 1")
        check("新建表单打开", s.js("return !!document.querySelector('.plugin-cmd')"), "")
        fill = s.js("""
const f=(label)=>{const x=[...document.querySelectorAll('.col-detail .field')]
  .find(e=>(e.querySelector('span')||{}).textContent?.trim()===label); return x?x.querySelector('input,textarea'):null};
const set=(el,v)=>{const P=el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(P,'value').set.call(el,v);
  el.dispatchEvent(new Event('input',{bubbles:true}));};
const n=f('名称'), c=f('命令');
if(!n||!c) return 'NO_FIELD';
set(n,'cfg_new_ui'); set(c,['/bin/echo','hi'].join(String.fromCharCode(10)));
return {name:n.value, cmd:c.value};""")
        check("能填名称和命令", isinstance(fill, dict) and fill.get("name") == "cfg_new_ui", fill)
        click_save(s)   # 插件表单没有自己的保存按钮，走一级卡的统一保存
        check("保存无报错", s.js("return window.__alerts.length") == 0, s.js("return window.__alerts"))
        st, lst = api("GET", "/api/tools")
        check("新插件出现在工具列表", any(x["name"] == "cfg_new_ui" for x in (lst or [])),
              [x["name"] for x in (lst or [])][:6])
        api("DELETE", "/api/plugins/cfg_new_ui")
        st, lst = api("GET", "/api/tools")
        check("已清理", not any(x["name"] == "cfg_new_ui" for x in (lst or [])), "")
    finally:
        s.close()
        api("DELETE", "/api/plugins/cfg_new_ui")


def dirty_dot():
    """未保存标记：标题后的小圆点。

    保存统一在一级卡（窄屏是底部操作条），二三级上没有保存按钮 ——
    但"改没改"是状态、不是动作，应该在正在改的那张卡上就能看到。
    """
    print("\n" + "=" * 62)
    print("第十二部分：未保存标记")
    print("=" * 62)

    # 自己开一个会话：跑到这里时 main 的那个可能已经因为累计耗时超时了
    s = Session(1600, 1000)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})

    def dots():
        return s.js("""return [...document.querySelectorAll('.card')]
            .filter(c=>c.querySelector('.dirty-dot'))
            .map(c=>c.className.trim());""")

    s.get(B + "/?settings=1&conv=51")
    time.sleep(2.5)
    check("刚打开时没有圆点", not dots(), dots())

    open_profile(s, "180-low")
    check("打开模型、没改 → 没有圆点", not dots(), dots())

    s.js("const it=[...document.querySelectorAll('.col-detail .item')].find(e=>/system/.test(e.textContent)); it&&it.click(); return !!it")
    time.sleep(1.0)
    check("打开 3 级、没改 → 没有圆点", not dots(), dots())

    s.js("""const ta=document.querySelector('.col-editor textarea');
      const set=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;
      set.call(ta, ta.value + ' '); ta.dispatchEvent(new Event('input',{bubbles:true})); return 1;""")
    time.sleep(0.5)
    d3 = dots()
    check("改了 3 级 → 3 级有圆点", any("col-editor" in c for c in d3), d3)
    # system 也是 profile 的字段，所以二级卡同时亮 —— 这是实话，不是重复
    check("改了 3 级 → 2 级也有圆点（system 属于 profile）", any("col-detail" in c for c in d3), d3)

    s.js("const b=document.querySelector('.col-editor .back'); b&&b.click(); return 1")
    time.sleep(0.7)
    check("退回 2 级后只剩 2 级的圆点",
          dots() and all("col-detail" in c for c in dots()), dots())

    click_save(s)
    check("保存后圆点消失", not dots(), dots())
    s.close()


def endpoint_flow():
    """接口：地址和凭据从模型里拆出来，独立注册。"""
    print("\n" + "=" * 62)
    print("第十三部分：接口")
    print("=" * 62)
    s = Session(1600, 1000)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
    try:
        s.get(B + "/?settings=1&conv=51")
        time.sleep(2.5)
        s.js("window.__alerts=[]; window.alert=(m)=>{window.__alerts.push(String(m))}; window.confirm=()=>true; return 1")

        secs = s.js("return [...document.querySelectorAll('.col-list .set-section')].map(e=>e.textContent.trim())")
        check("一级卡有「接口」分组", "接口" in secs, secs)

        st, eps = api("GET", "/api/endpoints")
        check("接口列表非空", len(eps or []) > 0, len(eps or []))
        ep = (eps or [])[0]

        # 模型里不再存地址
        st, profs = api("GET", "/api/profiles")
        check("模型都引用了接口", all(p.get("endpoint") for p in (profs or [])),
              [p["name"] for p in (profs or []) if not p.get("endpoint")])
        check("模型里没有残留的 base_url", all(not p.get("base_url") for p in (profs or [])), "")

        ok = s.js("""const x=[...document.querySelectorAll('.col-list .item.col')]
            .find(e=>e.querySelector('.item-title')?.textContent.trim()===%s);
          x&&x.click(); return !!x;""" % json.dumps(ep["name"]))
        time.sleep(1.2)
        check("点接口打开接口卡", ok, "")
        fields = s.js("""return [...document.querySelectorAll('.col-detail .field')]
            .map(f=>f.querySelector('span')?.textContent.trim());""")
        check("接口卡有来源/名称/地址/密钥/协议", fields == ["来源模板", "名称", "接口地址", "API Key", "协议"], fields)

        # 拉模型列表
        s.js("const b=[...document.querySelectorAll('.col-detail .btn')].find(x=>/拉取列表/.test(x.textContent)); b&&b.click(); return !!b")
        time.sleep(2.5)
        got = s.js("const m=document.querySelector('.col-detail .tool-cmd'); return m? m.textContent.trim() : null;")
        check("拉取列表能返回模型", isinstance(got, str) and len(got) > 0, (got or "")[:40])
        check("拉取没有报错", s.js("return window.__alerts.length") == 0, s.js("return window.__alerts"))

        # 新建接口 + 选来源预填
        s.js("const b=[...document.querySelectorAll('.col-detail .back')][0]||document.querySelector('.card-back'); b&&b.click(); return 1")
        time.sleep(0.8)
        s.js("const b=[...document.querySelectorAll('.item-new')].find(x=>/新建接口/.test(x.textContent)); b&&b.click(); return !!b")
        time.sleep(1.0)
        check("新建接口卡打开", s.js("""const t=document.querySelector('.col-detail .ef-label-t');
            return !!t && /新建接口/.test(t.textContent);"""), "")
        s.js("""const sel=[...document.querySelectorAll('.col-detail .field select')][0];
          sel.value='deepseek'; sel.dispatchEvent(new Event('change',{bubbles:true})); return 1;""")
        time.sleep(0.6)
        pre = s.js("""const g=(l)=>{const f=[...document.querySelectorAll('.col-detail .field')]
            .find(x=>(x.querySelector('span')||{}).textContent?.trim()===l); return f? f.querySelector('input,select'):null};
          const a=g('接口地址'), p=g('协议'); return {url:a?a.value:'', prov:p?p.value:''};""")
        check("选来源会预填地址", pre.get("url") == "https://api.deepseek.com/v1", pre)
        check("选来源会预填协议", pre.get("prov") == "openai", pre)

        # 撞名要拒绝（服务端）
        st, _ = api("POST", "/api/endpoints", {"name": ep["name"], "base_url": "http://x/v1"})
        check("同名接口是覆盖而不是新增", st == 200, st)
        api("POST", "/api/endpoints", {"name": ep["name"], "base_url": ep["base_url"],
                                       "api_key": ep.get("api_key", ""), "provider": ep.get("provider", ""),
                                       "source": ep.get("source", "")})
        # 被引用的接口不能删（先查模型层，再查配置层 —— 两句都得说清楚是谁在拦）
        st, d = api("DELETE", "/api/endpoints/" + ep["name"])
        check("被模型引用的接口不能删",
              st == 400 and "还有模型挂在这个接口下" in (d.get("error") or ""), f"{st} {d.get('error')}")
    finally:
        s.close()


def three_layers():
    """三层：接口（接哪儿）→ 模型（是什么）→ 配置（怎么用）。"""
    print("\n" + "=" * 62)
    print("第十四部分：三层结构与会话级思考强度")
    print("=" * 62)
    s = Session(1600, 1000)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
    # 这一节会临时改一个模型和一个接口；中途断言抛异常也要还原，
    # 否则真库上会留着 max_tokens=1234 这种探针值
    undo = []
    try:
        s.get(B + "/?settings=1&conv=51")
        time.sleep(2.5)
        secs = s.js("return [...document.querySelectorAll('.col-list .set-section')].map(e=>e.textContent.trim())")
        check("一级卡按三层分组", secs[:4] == ["常规配置", "接口", "模型", "模型配置"], secs)

        st, mdl = api("GET", "/api/models")
        check("模型层非空", len(mdl or []) > 0, len(mdl or []))
        check("每个模型都归属一个接口", all(m.get("endpoint") for m in (mdl or [])), "")
        m0 = mdl[0]
        check("模型的唯一键是 接口/id", m0["name"] == m0["endpoint"] + "/" + m0["id"], m0["name"])

        # 同一模型可以被多个配置共用 —— 这正是拆层要解决的重复配置
        st, profs = api("GET", "/api/profiles")
        names = [p.get("model") for p in (profs or [])]
        shared = [m for m in set(names) if names.count(m) > 1]
        check("存在多个配置共用一个模型", len(shared) > 0, shared[:2])

        # 配置里不再存上游 id / 窗口
        check("配置不再存上游模型名", all("/" in (p.get("model") or "") for p in (profs or [])),
              [p["name"] for p in (profs or []) if "/" not in (p.get("model") or "")])
        check("配置不再存窗口", all(not p.get("context_window") for p in (profs or [])), "")

        # 模型卡
        ok = s.js("""const x=[...document.querySelectorAll('.col-list .item.col')]
            .find(e=>e.querySelector('.item-title')?.textContent.trim()===%s);
          x&&x.click(); return !!x;""" % json.dumps(m0["id"]))
        time.sleep(1.2)
        check("点模型打开模型卡", ok, "")
        fields = s.js("""return [...document.querySelectorAll('.col-detail .field')]
            .map(f=>f.querySelector('span')?.textContent.trim());""")
        check("模型卡只有「模型事实」",
              fields[:4] == ["接口", "模型 id", "窗口大小 (tokens)", "输出上限 (tokens)"]
              and "支持的思考强度" in fields, fields)
        check("模型卡有图像开关",
              s.js("""return [...document.querySelectorAll('.col-detail .switch-row')]
                  .some(e=>/能接收图像/.test(e.textContent));"""), "")

        # —— 接口卡和模型卡都要能存 ——
        # 拆成三层之后这两层曾经只显示不保存（改了直接丢），
        # 现在它们和配置层走同一份改动清单：改 → 清单 → 确认 → 后端真变了。
        def save_sheet():
            s.js("const b=document.querySelector('.col-list .card-actions .btn.save'); b&&b.click(); return !!b")
            time.sleep(0.9)
            s.js("[...document.querySelectorAll('.sheet-ft .btn')]"
                 ".find(x=>/确认保存/.test(x.textContent)).click(); return 1")
            time.sleep(2.2)

        def put_value(sel, val):
            s.js("""const el=%s; const set=Object.getOwnPropertyDescriptor(
                el instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set;
                set.call(el, %s); el.dispatchEvent(new Event('input',{bubbles:true})); return el.value""" % (sel, json.dumps(val)))
            time.sleep(0.3)

        # 模型卡：改输出上限 → 存 → 读回
        st, models_now = api("GET", "/api/models")
        me = next(m for m in models_now if m["name"] == m0["name"])
        old_out = str(me.get("max_tokens") or "")
        undo.append(("model", me))
        put_value("document.querySelectorAll('.col-detail input')[2]", "1234")
        check("改模型卡字段后标题出现脏点", s.js("return !!document.querySelector('.col-detail .dirty-dot')"), "")
        s.js("const b=document.querySelector('.col-list .card-actions .btn.save'); b&&b.click(); return !!b")
        time.sleep(0.9)
        rows = s.js("return [...document.querySelectorAll('.sheet-grp-t, .sheet-row')].map(e=>e.textContent)")
        check("清单里列出模型改动", any("模型 ·" in (r or "") for r in (rows or [])), rows)
        s.js("[...document.querySelectorAll('.sheet-ft .btn')].find(x=>/确认保存/.test(x.textContent)).click(); return 1")
        time.sleep(2.2)
        st, after_m = api("GET", "/api/models")
        got = next((m.get("max_tokens") for m in after_m if m["name"] == m0["name"]), None)
        check("模型卡的改动真的落盘", got == 1234, got)
        check("存完脏点消失", s.js("return !document.querySelector('.col-detail .dirty-dot')"), "")
        put_value("document.querySelectorAll('.col-detail input')[2]", old_out)
        save_sheet()
        st, after_m = api("GET", "/api/models")
        got = next((m.get("max_tokens") for m in after_m if m["name"] == m0["name"]), None)
        check("模型卡改回去也落盘", got == (int(old_out) if old_out else None), got)

        # 接口卡：改地址 → 存 → 读回 → 改回
        s.js("""const x=[...document.querySelectorAll('.col-list .item.col')]
            .find(e=>e.querySelector('.item-title')?.textContent.trim()===%s);
          x&&x.click(); return !!x;""" % json.dumps(m0["endpoint"]))
        time.sleep(1.0)
        check("点接口打开接口卡",
              (s.js("return (document.querySelector('.col-detail .ef-label-t')||{}).textContent") or "")
              .startswith("接口 · "), "")
        st, eps = api("GET", "/api/endpoints")
        ep = next(e for e in eps if e["name"] == m0["endpoint"])
        old_url = ep["base_url"]
        undo.append(("endpoint", ep))
        put_value("document.querySelectorAll('.col-detail input')[1]", "http://127.0.0.1:18320/9/v1")
        save_sheet()
        st, eps = api("GET", "/api/endpoints")
        got_url = next(e["base_url"] for e in eps if e["name"] == m0["endpoint"])
        check("接口卡的改动真的落盘", got_url == "http://127.0.0.1:18320/9/v1", got_url)
        put_value("document.querySelectorAll('.col-detail input')[1]", old_url)
        save_sheet()
        st, eps = api("GET", "/api/endpoints")
        got_url = next(e["base_url"] for e in eps if e["name"] == m0["endpoint"])
        check("接口卡改回去也落盘", got_url == old_url, got_url)

        # 2 级卡互斥：接口卡开着时点工具，不能并排两张
        s.js("""const x=[...document.querySelectorAll('.col-list .item')].find(e=>{
            const t=e.querySelector('.item-title');
            return t && t.textContent.trim().startsWith('bash');});
          x&&x.click(); return !!x;""")
        time.sleep(1.0)
        check("打开工具时接口卡让位（2 级只有一张）",
              s.js("return document.querySelectorAll('.col-detail').length") == 1
              and (s.js("return (document.querySelector('.col-detail .ef-label-t')||{}).textContent") or "").startswith("tool"),
              s.js("return [...document.querySelectorAll('.col-detail .ef-label-t')].map(e=>e.textContent)"))

        # 配置卡的模型下拉
        s.js("const b=document.querySelector('.col-detail .back')||document.querySelector('.card-back'); b&&b.click(); return 1")
        time.sleep(0.8)
        s.js("""const x=[...document.querySelectorAll('.col-list .item.col')]
            .find(e=>e.querySelector('.item-title')?.textContent.trim()==='180-low'); x&&x.click(); return !!x;""")
        time.sleep(1.2)
        sel = s.js("""const f=[...document.querySelectorAll('.col-detail .field')]
            .find(x=>(x.querySelector('span')||{}).textContent?.trim()==='模型');
          const sel=f?.querySelector('select'); return sel? {v:sel.value, n:sel.options.length}:null;""")
        check("配置卡用模型下拉", bool(sel) and sel["n"] == len(mdl) + 1, sel)

        # 会话级思考强度：临时覆盖，不落盘
        st, profiles = api("GET", "/api/profiles")
        before = next(p for p in profiles if p["name"] == "180-low")
        s.get(B + "/?conv=51")
        time.sleep(2.5)
        picks = s.js("return [...document.querySelectorAll('.composer .pick select')].length")
        check("composer 有两个选择器（配置 + 强度）", picks == 2, picks)
        opts = s.js("""const sel=[...document.querySelectorAll('.composer .pick select')][1];
          return [...sel.options].map(o=>o.value);""")
        check("强度选择器含「默认」与各档位", opts[0] == "" and "high" in opts and "none" in opts, opts)
        # 比的是"当前选中的那个配置"的默认值 —— composer 从 localStorage 恢复配置，
        # 不一定是上面查的 180-low
        cur_name = s.js("return [...document.querySelectorAll('.composer .pick select')][0].value")
        cur_prof = next((p for p in profiles if p["name"] == cur_name), {})
        label = s.js("return [...document.querySelectorAll('.composer .pick select')][1].options[0].textContent")
        check("默认档显示当前配置的默认值",
              (cur_prof.get("reasoning_effort") or "none") in label, f"{cur_name}: {label}")
        # 改一下，确认没写回配置
        s.js("""const sel=[...document.querySelectorAll('.composer .pick select')][1];
          sel.value='high'; sel.dispatchEvent(new Event('change',{bubbles:true})); return 1;""")
        time.sleep(0.5)
        st, after = api("GET", "/api/profiles")
        same = next(p for p in after if p["name"] == "180-low")
        check("会话里改强度不写回配置", same.get("reasoning_effort") == before.get("reasoning_effort"),
              f"{before.get('reasoning_effort')} → {same.get('reasoning_effort')}")

        # 多模态提示：标记要跟**当前选中的配置**所用的模型一致，
        # 不是"库里存在某个纯文字模型"（那种写法现在会因为 deepseek-v4-pro 而误判）
        hint = s.js("const n=document.querySelector('.composer .no-vision'); return n? n.textContent.trim():null;")
        cur_model = next((m for m in (models_now or []) if m.get("name") == cur_prof.get("model")), {})
        want = None if "image" in (cur_model.get("input") or []) else "文字"
        check("composer 上的模态标记跟当前配置的模型一致", hint == want,
              f"{cur_prof.get('name')} · {cur_model.get('id')} 只有 {cur_model.get('input')} → {hint}")
    finally:
        # 断言中途失败也要把探针值还原（这一节改的是真模型 / 真接口）
        for kind, orig in undo:
            try:
                api("POST", "/api/" + kind + "s", orig)
            except Exception as e:
                print(f"  ! 还原 {kind} {orig.get('name')} 失败：{e}")
        s.close()


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
        narrow_flow()
        delete_flow()
        deep_links()
        dirty_dot()
        endpoint_flow()
        three_layers()
        task_deeplink()
        new_plugin_ui()
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
