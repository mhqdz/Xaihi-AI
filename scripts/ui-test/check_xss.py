#!/usr/bin/env python3
"""XSS 黑盒测试：把恶意 HTML 直接塞进库里，看渲染层会不会执行。

绕过后端直接写 messages 表，模拟「模型输出了带攻击载荷的内容」这种最坏情况。
判定标准：window.__xss 没被设置、DOM 里没有 script、没有 onerror 一类的内联事件、
没有 javascript: 协议的链接。
"""
import sys, os, time, sqlite3, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req
from dbtool import new_conv, drop_conv

BASE = "http://127.0.0.1:18400"
DB = os.path.expanduser("~/.data/agentd/agentd.db")

PAYLOADS = [
    '<img src=x onerror="window.__xss=\'img\'">',
    '<script>window.__xss="script"</script>',
    '[点我](javascript:window.__xss="js")',
    '<svg onload="window.__xss=\'svg\'"></svg>',
    '<iframe src="javascript:window.__xss=\'iframe\'"></iframe>',
    '<a href="javascript:window.__xss=\'a\'">链接</a>',
    '![图](x" onerror="window.__xss=\'mdimg\')',
    '<details open ontoggle="window.__xss=\'toggle\'">x</details>',
]

fails = []
def check(name, ok, detail=""):
    print(("  ✓ " if ok else "  ✗ ") + name + (f"  {detail}" if detail else ""))
    if not ok:
        fails.append(name)

cid = new_conv("ui-xss")
c = sqlite3.connect(DB)
now = time.strftime("%Y-%m-%dT%H:%M:%S")
seq = 0
for p in PAYLOADS:
    for role in ("user", "assistant"):      # 两种角色都要过渲染
        seq += 1
        c.execute(
            "insert into messages(conv_id,seq,role,content,status,origin,created_at) values(?,?,?,?,?,?,?)",
            (int(cid), seq, role, p, "done", "user", now),
        )
c.commit(); c.close()

s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
s.get(BASE + f"/?conv={cid}")
time.sleep(2.5)

r = s.js("""
return {
  xss: window.__xss || null,
  scripts: document.querySelectorAll('.prose script').length,
  inline: [...document.querySelectorAll('.prose *')].filter(e =>
      [...e.attributes].some(a => a.name.startsWith('on'))).length,
  jsHref: [...document.querySelectorAll('.prose a')].filter(a =>
      (a.getAttribute('href') || '').replace(/\\s/g, '').toLowerCase().startsWith('javascript:')).length,
  iframes: document.querySelectorAll('.prose iframe').length,
  msgCount: document.querySelectorAll('.msg').length,
  sample: (document.querySelector('.prose') || {}).textContent?.slice(0, 90) || ''
};
""")
print(json.dumps(r, ensure_ascii=False, indent=1))

check("载荷没有执行（window.__xss 未被设置）", r["xss"] is None, r["xss"])
check("DOM 里没有 script", r["scripts"] == 0, r["scripts"])
check("没有内联事件属性", r["inline"] == 0, r["inline"])
check("没有 javascript: 链接", r["jsHref"] == 0, r["jsHref"])
check("没有 iframe 注入", r["iframes"] == 0, r["iframes"])
check("消息确实渲染出来了（不是整页崩掉）", r["msgCount"] >= len(PAYLOADS), r["msgCount"])

errs = s.js("return (window.__errs||[]).slice(0,3)")
s.close()
drop_conv(cid)

print("\n失败:", "、".join(fails) if fails else "无")
sys.exit(1 if fails else 0)
