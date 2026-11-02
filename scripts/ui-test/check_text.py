#!/usr/bin/env python3
"""文本边界：把极端的消息内容直接塞进库，看渲染会不会溢出、卡死或报错。

覆盖 emoji、RTL（阿拉伯文）、无空格超长串、控制字符、超长 URL、混排。
判定：无横向溢出、无 JS 错误、消息都渲染出来、正文没把布局撑破。
"""
import sys, os, time, sqlite3, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req
from dbtool import new_conv, drop_conv

BASE = "http://127.0.0.1:18400"
DB = os.path.expanduser("~/.data/agentd/agentd.db")

CASES = [
    ("emoji", "🎉🎊✨🚀🦊 全部渲染 🌈"),
    ("rtl", "مرحبا بالعالم — 这是阿拉伯文和中文混排"),
    ("无空格长串", "a" * 400),
    ("控制字符", "前\x00中\x01后\x07尾"),
    ("超长 URL", "https://example.com/" + "x" * 240),
    ("中英混排", "这是中文English混合123数字，还有标点！？；："),
]

fails = []
def check(name, ok, detail=""):
    print(("  ✓ " if ok else "  ✗ ") + name + (f"  {detail}" if detail else ""))
    if not ok:
        fails.append(name)

cid = new_conv("ui-text")
c = sqlite3.connect(DB)
now = time.strftime("%Y-%m-%dT%H:%M:%S")
for i, (_, body) in enumerate(CASES, start=1):
    c.execute("insert into messages(conv_id,seq,role,content,status,origin,created_at) values(?,?,?,?,?,?,?)",
              (int(cid), i, "user", body, "done", "user", now))
c.commit(); c.close()

s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
s.js("window.__errs=[]; window.addEventListener('error',e=>window.__errs.push(String(e.message))); return 1")
s.get(BASE + f"/?conv={cid}")
time.sleep(2.8)

r = s.js("""
const ow = [document.documentElement.scrollWidth, innerWidth];
const ms = [...document.querySelectorAll('.msg')];
const wide = ms.filter(m => {
  const r = m.getBoundingClientRect();
  return r.right > innerWidth + 1 || r.left < -1;
}).length;
return {sw: ow[0], iw: ow[1], msgs: ms.length, wide,
        errs: (window.__errs || []).slice(0, 4),
        text: (document.querySelector('.thread') || {}).innerText ? 'ok' : 'empty'};
""")
print(json.dumps(r, ensure_ascii=False))

check("消息全部渲染", r["msgs"] >= len(CASES), r["msgs"])
check("无横向溢出", r["sw"] <= r["iw"] + 1, f"{r['sw']} vs {r['iw']}")
check("没有消息卡越界", r["wide"] == 0, r["wide"])
check("无 JS 错误", r["errs"] == [], r["errs"])
check("正文区域有内容", r["text"] == "ok")

s.close()
drop_conv(cid)
print("\n失败:", "、".join(fails) if fails else "无")
sys.exit(1 if fails else 0)
