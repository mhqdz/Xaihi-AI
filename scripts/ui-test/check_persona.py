#!/usr/bin/env python3
"""人格编辑器回归：能打开、能新建、能保存、能读回。

用临时名字 uitest，跑完删掉，不动真实人格。
"""
import sys, os, time, json, urllib.request
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"
FAIL = []


def check(name, cond, extra=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (f"  {extra}" if extra else ""))
    if not cond:
        FAIL.append(name)


def api(path):
    with urllib.request.urlopen(BASE + path, timeout=20) as r:
        return json.loads(r.read())


s = Session(1500, 1400)
req("POST", f"/session/{s.id}/window/rect", {"width": 1500, "height": 1400})
s.get(BASE + "/?settings=1&conv=51&profile=Xaihi")
time.sleep(2.5)

check("配置卡打开", s.js("return !!document.querySelector('.col-editor, .card .link-row')") is True)

# 点「查看 / 编辑人格」
clicked = s.js("""const r=[...document.querySelectorAll('.link-row')]
  .find(e=>e.textContent.includes('查看 / 编辑人格'));
  if(!r) return false; r.click(); return true""")
time.sleep(1.2)
check("编辑器打开", s.js("return !!document.querySelector('.col-editor textarea')") is True)
check("内容非空", (s.js("const t=document.querySelector('.col-editor textarea'); return t?t.value.length:0") or 0) > 20)

# 关掉 → 新建 → 填 → 保存
s.js("""const b=[...document.querySelectorAll('.col-editor button')]
  .find(x=>/关闭|×/.test(x.textContent)); if(b) b.click(); return 1""")
time.sleep(0.6)
s.js("""const r=[...document.querySelectorAll('.link-row')].find(e=>e.textContent.includes('新建人格'));
  if(r) r.click(); return 1""")
time.sleep(0.6)
s.js("""const inp=document.querySelector('.col-editor input');
  const set=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;
  set.call(inp,'uitest'); inp.dispatchEvent(new Event('input',{bubbles:true}));
  const ta=document.querySelector('.col-editor textarea');
  const setT=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;
  setT.call(ta,'临时人格：只用于界面回归测试。'); ta.dispatchEvent(new Event('input',{bubbles:true}));
  return 1""")
time.sleep(0.4)
s.js("""const b=[...document.querySelectorAll('.col-editor button')]
  .find(x=>x.textContent.includes('保存人格')); if(!b) return false; b.click(); return true""")
time.sleep(1.5)

try:
    body = api("/api/personas/uitest")["body"]
except Exception as e:
    body = ""
check("保存后能读回", "界面回归" in body, body[:30])

# 清理
path = os.path.expanduser("~/.data/xaihi/personas/uitest.md")
if os.path.exists(path):
    os.remove(path)
s.close()

print()
if FAIL:
    print("✗ 失败:", ", ".join(FAIL)); sys.exit(1)
print("✓ 全部通过")
