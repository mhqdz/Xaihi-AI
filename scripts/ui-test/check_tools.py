import sys, os, time, json
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

FAIL = []
def check(name, cond, extra=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (f"  {extra}" if extra else ""))
    if not cond: FAIL.append(name)

BASE = "http://127.0.0.1:18400"
s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
s.get(BASE + "/?conv=51")
time.sleep(2)

# 1) 工具组基本结构
groups = s.js("return document.querySelectorAll('.toolnote').length")
heads = s.js("return document.querySelectorAll('.tn-hd').length")
rows = s.js("return document.querySelectorAll('.tn-row').length")
check("有工具组", groups > 0, f"groups={groups}")
check("每个组恰好一个 Tool 头", heads == groups, f"heads={heads} groups={groups}")
check("默认收拢（无工具行）", rows == 0, f"rows={rows}")
check("每个组都有四角括号标记", s.js("return [...document.querySelectorAll('.toolnote')].every(g=>g.querySelector('.tn-c.tl'))") == True)

# 2) 展开第一组
s.js("const h=document.querySelector('.tn-hd'); h.scrollIntoView({block:'center'}); h.click(); return 1")
time.sleep(0.4)
rows2 = s.js("return document.querySelectorAll('.tn-row').length")
tags = s.js("return [...document.querySelectorAll('.tn-no')].map(e=>e.textContent)")
names = s.js("return [...document.querySelectorAll('.tn-name')].map(e=>e.textContent)")
check("点 Tool 头能展开该组", rows2 > 0, f"rows={rows2}")
check("编号是 No.NNN 或 ··", all(t == '··' or (t.startswith('No.') and len(t) == 6) for t in tags), str(tags[:4]))
check("每行有工具名", all(n.strip() for n in names), str(names[:4]))
hd = s.js("return document.querySelector('.tn-hd .tn-stats').textContent")
check("组头统计含编号与字数", ('No.' in hd and ('字' in hd or ' · ' in hd)), hd)
check("组头可折叠标记在两个方向都换", s.js("return document.querySelector('.tn-hd .tn-fold').textContent") in ('▾', '▸'))

# 3) 再收起
s.js("document.querySelector('.tn-hd').click(); return 1")
time.sleep(0.3)
check("再点收起", s.js("return document.querySelectorAll('.tn-row').length") == 0)

# 4) memo：切换展开态时不应重渲染全部历史消息卡
n0 = s.js("return (window.__renders && window.__renders.msg) || 0")
s.js("window.__renders = {msg:0, tool:0, note:0}; return 1")
s.js("document.querySelector('.tn-hd').click(); return 1")
time.sleep(0.4)
n1 = s.js("return window.__renders.msg")
t1 = s.js("return window.__renders.tool")
check("展开工具组只重渲染该组", t1 >= 1 and t1 <= 2, f"toolRenders={t1}")
check("切换展开态不重渲染历史消息卡（memo 生效）", n1 == 0, f"msgRenders={n1} (before={n0})")

# 5) 收起思考后定位（MessageCard 内的按钮）
s.js("document.querySelector('.tn-hd').click(); return 1")
time.sleep(0.3)
s.js("const d=[...document.querySelectorAll('details.fold')][0]; if(d){d.open=true; d.querySelector('.fold-close').click()} return 1")
time.sleep(0.5)
check("收起思考按钮不报错，思考块已关闭", s.js("const d=[...document.querySelectorAll('details.fold')][0]; return d ? d.open : true") == False)

# 6) 阅读位置编号仍连续
seqs = s.js("return [...document.querySelectorAll('[data-seq]')].map(e=>+e.dataset.seq)")
# seq 是数据库编号：可见消息之间会跳过 tool 的号，所以只要求严格递增
check("data-seq 严格递增（唯一事实来源）", bool(seqs) and all(b > a for a, b in zip(seqs, seqs[1:])), f"n={len(seqs)} head={seqs[:6]}")

# 7) 无 JS 错误
errs = s.js("return (window.__errs||[]).slice(0,3)")
check("交互过程无 JS 错误", not errs, str(errs))

s.shot("/tmp/uitest/step2_toolgroup.png")
print("失败:", "、".join(FAIL) if FAIL else "无")
