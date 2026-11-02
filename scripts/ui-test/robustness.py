#!/usr/bin/env python3
"""黑盒健壮性测试：异常输入、边界状态、响应式切换。
只观察，不假设实现。任何 JS 报错 / 布局溢出 / 卡死都算问题。
"""
import sys, os, time, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"
fails = []
def check(name, ok, detail=""):
    print(("  ✓ " if ok else "  ✗ ") + name + (f"  {detail}" if detail else ""))
    if not ok:
        fails.append((name, detail))

def new_session(w=1600, h=1000):
    s = Session(w, h)
    req("POST", f"/session/{s.id}/window/rect", {"width": w, "height": h})
    return s

def js_errs(s):
    return s.js("return (window.__errs||[]).slice(0,5)")

def install_err_hook(s):
    s.js("window.__errs=[]; window.addEventListener('error',e=>window.__errs.push(String(e.message))); "
         "window.addEventListener('unhandledrejection',e=>window.__errs.push('rej:'+String(e.reason))); return 1")

def overflow(s):
    return s.js("return [document.documentElement.scrollWidth, innerWidth]")

# ── 1. 不存在的会话
print("\n[1] 打开不存在的会话 ?conv=999999")
s = new_session(); s.get(BASE + "/?conv=999999"); time.sleep(2.2)
install_err_hook(s)
check("不崩（仍有 DOM）", s.js("return document.body.children.length > 0") is True)
check("无 JS 错误", js_errs(s) == [], js_errs(s))
ow = overflow(s)
check("无横向溢出", ow[0] <= ow[1] + 1, ow)
s.shot("/tmp/uitest/rb_missing.png"); s.close()

# ── 2. 空会话（新建）
print("\n[2] 新建空会话")
s = new_session(); s.get(BASE + "/"); time.sleep(2.0)
install_err_hook(s)
check("显示空态引导", s.js("return !!document.querySelector('.col.empty, .empty')") is True)
check("发送按钮禁用", s.js("return document.querySelector('.send').disabled === true") is True)
check("无 JS 错误", js_errs(s) == [], js_errs(s))
s.close()

# ── 3. 超长输入（10000 字）
print("\n[3] 输入 10000 字不发送")
s = new_session(); s.get(BASE + "/?conv=118"); time.sleep(2.2)
install_err_hook(s)
s.js("""const t=document.querySelector('.composer textarea');
const set=Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype,'value').set;
set.call(t, 'x'.repeat(10000)); t.dispatchEvent(new Event('input',{bubbles:true})); return 1""")
time.sleep(0.8)
h = s.js("return Math.round(document.querySelector('.composer').getBoundingClientRect().height)")
ow = overflow(s)
check("输入框不无限长高（≤ 60vh）", h <= 620, f"composer h={h}")
check("无横向溢出", ow[0] <= ow[1] + 1, ow)
check("无 JS 错误", js_errs(s) == [], js_errs(s))
s.shot("/tmp/uitest/rb_longtext.png"); s.close()

# ── 4. 快速连点发送（防重复提交）
print("\n[4] 快速连点发送 5 次")
s = new_session(); s.get(BASE + "/?conv=118"); time.sleep(2.2)
install_err_hook(s)
s.js("""const t=document.querySelector('.composer textarea');
const set=Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype,'value').set;
set.call(t, 'rb-test'); t.dispatchEvent(new Event('input',{bubbles:true})); return 1""")
time.sleep(0.3)
for _ in range(5):
    s.js("const b=document.querySelector('.send'); if(b && !b.disabled) b.click(); return 1")
    time.sleep(0.05)
time.sleep(2.0)
running = json.load(__import__('urllib.request', fromlist=['x']).urlopen(BASE + "/api/running"))
check("只起一轮（running ≤ 1）", len(running) <= 1, running)
check("无 JS 错误", js_errs(s) == [], js_errs(s))
# 停掉
for cid in running:
    try:
        __import__('urllib.request', fromlist=['x']).urlopen(
            __import__('urllib.request', fromlist=['x']).Request(BASE + f"/api/conv/{cid}/stop", data=b"{}", method="POST"), timeout=10).read()
    except Exception:
        pass
s.close()

# ── 5. 响应式：窄 → 宽 → 窄 连续切换
print("\n[5] 响应式来回切换 900 ↔ 1600")
s = new_session(900, 1000); s.get(BASE + "/?conv=51"); time.sleep(2.4)
install_err_hook(s)
bad = []
for w in (1600, 900, 1600, 900):
    req("POST", f"/session/{s.id}/window/rect", {"width": w, "height": 1000}); time.sleep(1.0)
    ow = overflow(s)
    if ow[0] > ow[1] + 1: bad.append((w, ow))
check("四次切换都无横向溢出", not bad, bad)
check("无 JS 错误", js_errs(s) == [], js_errs(s))
s.close()

# ── 6. 设置面板 Esc / 深链
print("\n[6] 设置面板 Esc 与深链")
s = new_session(); s.get(BASE + "/?settings=1&conv=51"); time.sleep(2.2)
install_err_hook(s)
check("深链打开设置", s.js("return !!document.querySelector('.set-cols')") is True)
s.js("document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true})); return 1"); time.sleep(0.6)
check("Esc 关闭设置", s.js("return !document.querySelector('.set-cols')") is True)
check("URL 里 settings 参数被清掉", s.js("return location.search.includes('settings')") is False, s.js("return location.search"))
check("无 JS 错误", js_errs(s) == [], js_errs(s))
s.close()

# ── 7. 标尺边界：全部未加载时点击
print("\n[7] 标尺：跳转后立刻再点未加载轮")
s = new_session(); s.get(BASE + "/?conv=51"); time.sleep(2.4)
install_err_hook(s)
s.js("document.querySelectorAll('.scale i')[0].click(); return 1"); time.sleep(1.8)
s.js("document.querySelectorAll('.scale i')[22].click(); return 1"); time.sleep(1.8)
check("两次跨窗口跳转后无 JS 错误", js_errs(s) == [], js_errs(s))
check("标尺仍有 cur", s.js("return document.querySelectorAll('.scale i.cur').length") == 1)
s.close()

print(f"\n=== 汇总: {len(fails)} 个问题 ===")
for n, d in fails:
    print("  ✗", n, d)
sys.exit(1 if fails else 0)
