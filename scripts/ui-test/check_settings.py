import sys, os, time, json, urllib.request
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"
FAIL = []
def check(name, cond, extra=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (f"  {extra}" if extra else ""))
    if not cond: FAIL.append(name)

def api(path):
    with urllib.request.urlopen(BASE + path, timeout=30) as r:
        return json.loads(r.read())

def set_input(js_find, value):
    return s.js("""const el=(""" + js_find + """);
if(!el) return null;
const set=Object.getOwnPropertyDescriptor(el instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set;
set.call(el, """ + json.dumps(value) + """);
el.dispatchEvent(new Event('input',{bubbles:true}));
el.dispatchEvent(new Event('change',{bubbles:true}));
return el.value""")

s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
s.get(BASE + "/?settings=1&conv=51")
time.sleep(2.2)

# ---------- 面板装配 ----------
check("设置面板打开", s.js("return !!document.querySelector('.settings .set-cols')") == True)
check("初始只有列表卡", s.js("return [...document.querySelectorAll('.set-cols > .card')].map(e=>e.className)") == ['card col-list is-top'])
check("列表卡头是 SETTINGS 标签", 'SETTINGS' in (s.text('.col-list .ef-label-t') or ''))
check("无脏状态时没有保存/取消", s.js("return document.querySelectorAll('.col-list .card-actions').length") == 0)

# ---------- 常规配置：改 → 保存/取消出现 → 取消回滚 ----------
days_lab = "document.querySelectorAll('.col-list label.field')[1].querySelector('input')"
before = s.js("return " + days_lab + ".value")
set_input(days_lab, '31')
time.sleep(0.3)
btns = s.js("return [...document.querySelectorAll('.col-list .card-actions .btn')].map(b=>b.textContent)")
check("改数字后出现 取消/保存", btns == ['取消', '保存'], str(btns))
s.js("document.querySelectorAll('.col-list .card-actions .btn')[0].click(); return 1")
time.sleep(0.4)
after = s.js("return " + days_lab + ".value")
check("取消把值回滚", after == before, f"{before} -> {after}")
check("回滚后按钮消失", s.js("return document.querySelectorAll('.col-list .card-actions').length") == 0)

# ---------- 常规配置：保存真的落库（改 31 再改回）----------
# 保存走"改动清单"：点保存先弹清单，确认后才写后端
def save_all():
    s.js("[...document.querySelectorAll('.col-list .card-actions .btn, .col-list .title-actions .btn')]"
         ".find(b=>b.textContent==='保存').click(); return 1")
    time.sleep(0.8)
    s.js("[...document.querySelectorAll('.sheet-ft .btn')].find(x=>/确认保存/.test(x.textContent)).click(); return 1")
    time.sleep(2.0)

set_input(days_lab, '31')
time.sleep(0.3)
save_all()
check("保存后后端生效", api('/api/settings').get('auto_archive_days') == 31, str(api('/api/settings')))
set_input(days_lab, str(before))
time.sleep(0.3)
save_all()
check("改回后后端复原", api('/api/settings').get('auto_archive_days') == int(before), str(api('/api/settings')))
check("保存后回到干净状态（没有保存/取消）", s.js("return document.querySelectorAll('.card-actions .btn').length") == 0)

# ---------- 接口卡：打开 → 改 API Key → 取消 ----------
s.js("[...document.querySelectorAll('.col-list .item.col')][0].click(); return 1")
time.sleep(0.6)
check("点接口出现详情卡", s.js("return !!document.querySelector('.col-detail')") == True)
check("列表与详情两级", s.js("return [...document.querySelectorAll('.set-cols > .card')].map(e=>e.className.split(' ')[1])") == ['col-detail', 'col-list'])
key = "[...document.querySelectorAll('.col-detail label.field input')].find(i=>i.type==='password')"
old = s.js("return " + key + ".value")
set_input(key, 'test-key-123')
time.sleep(0.3)
check("改 API Key 后出现保存/取消", s.js("return document.querySelectorAll('.col-list .card-actions .btn').length") == 2)
s.js("[...document.querySelectorAll('.col-list .card-actions .btn')].find(b=>b.textContent==='取消').click(); return 1")
time.sleep(0.4)
check("取消后 API Key 回滚", s.js("return " + key + ".value") == old)

# ---------- 工具卡 + 三级编辑 ----------
s.js("document.querySelector('.col-detail') && [...document.querySelectorAll('.col-list .item')].find(e=>e.textContent.includes('bash')).click(); return 1")
time.sleep(0.6)
check("点工具出现工具卡", s.js("return !!document.querySelector('.col-detail')") == True)
check("工具卡与接口卡互斥（只有一张 2 级卡）", s.js("return document.querySelectorAll('.col-detail').length") == 1)
s.js("document.querySelector('.col-detail .item').click(); return 1")   # 说明 → 三级
time.sleep(0.6)
check("说明进入三级编辑卡", s.js("return !!document.querySelector('.col-editor')") == True)
check("三级时同时有三张卡", s.js("return document.querySelectorAll('.set-cols > .card').length") == 3)
check("三张卡时列宽档位是 three", 'three' in (s.js("return document.querySelector('.set-cols').className") or ''))
check("three 档编辑器加宽 >400", (s.js("return Math.round(document.querySelector('.col-editor').getBoundingClientRect().width)") or 0) > 400)
area = "document.querySelector('.col-editor .set-area')"
old_txt = s.js("return " + area + " ? " + area + ".value : ''")
set_input(area, old_txt + '\n测试追加')
time.sleep(0.3)
check("改工具说明后出现保存/取消", s.js("return document.querySelectorAll('.col-list .card-actions .btn').length") == 2)
# 字数栏读的是 textarea 的 value.length，改完必须跟着变
foot = s.js("return (document.querySelector('.col-editor .set-col-foot')||{}).textContent")
cnt = s.js("return " + area + ".value.length")
check("字数栏跟随文本", foot == f"{cnt} 字", f"{foot} vs {cnt}")
# 保存按钮只有一个（在一级卡），所以"取消"是放弃全部：三级卡跟着关掉
s.js("[...document.querySelectorAll('.col-list .card-actions .btn')].find(b=>b.textContent==='取消').click(); return 1")
time.sleep(0.4)
check("取消后三级卡关闭（取消=放弃全部）", s.js("return !document.querySelector('.col-editor')"))
check("取消后没有残留的保存/取消", s.js("return document.querySelectorAll('.card-actions .btn').length") == 0)

# ---------- 运行中提示（localStorage，前端专属）----------
orig = s.js("return localStorage.getItem('ui.quips')")
ta = "document.querySelector('.col-list textarea.ui-quips')"
set_input(ta, '测试一行')
time.sleep(0.3)
saved = s.js("return localStorage.getItem('ui.quips')")
check("改提示写入 localStorage", saved == '["测试一行"]', str(saved))
if orig:
    s.js("localStorage.setItem('ui.quips', " + json.dumps(orig) + "); return 1")
else:
    s.js("localStorage.removeItem('ui.quips'); return 1")

# ---------- 深链：?profile=&field= 打开即"干净"（editSnap 原始设计）----------
s.get(BASE + "/?settings=1&conv=51&profile=180-low&field=system")
time.sleep(3.0)
check("深链打开 system 编辑卡", s.js("return (document.querySelector('.col-editor .ef-label-t')||{}).textContent") == 'system')
check("深链打开三张卡且为 three", s.js("return document.querySelectorAll('.set-cols > .card').length") == 3
      and 'three' in (s.js("return document.querySelector('.set-cols').className") or ''))
check("深链打开没有保存/取消（不是脏的）", s.js("return document.querySelectorAll('.card-actions .btn').length") == 0)
set_input("document.querySelector('.col-editor .set-area')", '改一下')
time.sleep(0.3)
check("深链后再改才出现保存/取消", s.js("return document.querySelectorAll('.card-actions .btn').length") == 2)
s.js("[...document.querySelectorAll('.card-actions .btn')].find(b=>b.textContent==='取消').click(); return 1")
time.sleep(0.3)
check("取消后回到干净状态", s.js("return document.querySelectorAll('.card-actions .btn').length") == 0)

# ---------- 脏 localStorage（ui.quips="null"）不能白屏 ----------
s.js("localStorage.setItem('ui.quips','null'); return 1")
s.get(BASE + "/?conv=51")
time.sleep(2.5)
check("ui.quips 是脏数据时页面照常渲染", s.js("return document.getElementById('root').children.length") == 1)
check("脏数据时仍显示会话正文", (s.js("return document.querySelectorAll('.msg').length") or 0) > 0)
s.js("localStorage.removeItem('ui.quips'); return 1")

# ---------- 无 JS 错误 ----------
errs = s.js("return (window.__errs||[]).slice(0,3)")
check("无 JS 错误", not errs, str(errs))
s.shot("/tmp/uitest/step3_settings.png")
print("失败:", "、".join(FAIL) if FAIL else "无")
s.close()
