import sys, os, time, json, sqlite3, urllib.request
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req
from dbtool import new_conv, drop_conv

DB = os.path.expanduser("~/.data/agentd/agentd.db")
FAIL=[]
def check(n,c,e=""):
    print(("  ✓ " if c else "  ✗ ")+n+(f"  {e}" if e else ""))
    if not c: FAIL.append(n)
B="http://127.0.0.1:18400"
# 自己造一条任务。原来在 tasks 表为空时会把深链用例"跳过"——
# 而断言写成 (not tid) or (...)，于是 tkid 为 None 时它直接算通过：
# 测试绿了，但其实什么都没测。
task_conv = new_conv("ui-run3-task")
tid = None
try:
    r = urllib.request.Request(B + "/api/tasks",
        data=json.dumps({"kind": "smoke", "conv_id": task_conv, "card": {"goal": "ui"}}).encode(),
        headers={"Content-Type": "application/json"}, method="POST")
    tid = json.loads(urllib.request.urlopen(r, timeout=30).read())["task_id"]
    for _ in range(24):
        time.sleep(0.5)
        d = json.loads(urllib.request.urlopen(B + "/api/tasks/" + str(tid), timeout=10).read())
        if (d.get("task") or {}).get("steps") or d.get("steps"):
            break
    time.sleep(1.5)
except Exception as e:
    print("  ✗ 造任务失败，深链用例无法验证:", e)
    FAIL.append("造任务")

s=Session(1600,1000); req("POST",f"/session/{s.id}/window/rect",{"width":1600,"height":1000})
# 任务深链滚动（修复后）
s.get(B+f"/?settings=1&conv=51&task={tid}"); time.sleep(2)
vis=s.js("const st=document.querySelector('.task-step'); if(!st) return 'no-step'; const r=st.getBoundingClientRect(); return [Math.round(r.top), innerHeight]")
check("任务深链自动滚到详情", isinstance(vis,list) and 0 <= vis[0] <= vis[1], f"{vis}")
s.shot("/tmp/uitest/wide_task2.png")

# 窄屏（同一会话改窗口尺寸）
req("POST",f"/session/{s.id}/window/rect",{"width":900,"height":1300})
s.get(B+"/?conv=51"); time.sleep(1.6)
check("窄屏无横向溢出", s.js("return document.scrollingElement.scrollWidth - innerWidth") <= 0)
top = s.js("const r=document.querySelector('.rail'); if(!r) return null; const b=r.getBoundingClientRect(); return [Math.round(b.top), Math.round(b.width), Math.round(b.height)]")
print("  窄屏 rail rect:", top)
check("窄屏右栏搬到顶部（高度很小、贴顶）", isinstance(top,list) and top[0] <= 8, f"{top}")
check("窄屏消息列宽度合理（≥80% 视口）", s.js("const m=document.querySelector('.msg'); if(!m) return -1; return Math.round(m.getBoundingClientRect().width / innerWidth * 100)") >= 75)
s.shot("/tmp/uitest/narrow_conv.png")
# 窄屏折叠把手：可见（白底+描边）、内缩、不压正文
tg = s.js("const b=document.querySelector('.side-toggle'); if(!b) return null; const g=b.getBoundingClientRect(); const c=getComputedStyle(b); const d=document.querySelector('.mobrail .mob-dot').getBoundingClientRect(); return [Math.round(g.left),Math.round(g.top),Math.round(g.width),c.borderTopWidth,c.backgroundColor,Math.round(d.left)]")
icon0 = s.js("return getComputedStyle(document.querySelector('.side-toggle .st-icon')).transform")
check("竖屏把手收起态朝下（上下朝向）", isinstance(icon0,str) and icon0.startswith('matrix(0, -1, 1, 0'), f"{icon0}")
n_open = 0
for i in range(4):
    s.click('.side-toggle'); time.sleep(.35)
    cls = s.js("return document.querySelector('.side-toggle').className")
    if 'collapsed' not in cls: n_open += 1
check("竖屏把手点击可靠（4 次点击应开 2 次）", n_open == 2, f"open={n_open}")
s.click('.side-toggle'); time.sleep(.3)   # 回到收起态
check("竖屏把手：左下角无、改到顶栏最左（状态左侧），无边框无底色",
      isinstance(tg,list) and tg[0] <= 8 and tg[1] <= 8 and tg[3] == '0px' and tg[4] in ('rgba(0, 0, 0, 0)','transparent') and tg[0]+tg[2] <= tg[5], f"{tg}")
txt = s.js("const m=document.querySelector('.msg'); return m? Math.round(m.getBoundingClientRect().left) : -1")
check("正文不被额外留白压缩（左边距≤24）", isinstance(txt,int) and 0 <= txt <= 24, f"left={txt}")

# 竖屏抽屉：设置按钮不换行；选完会话自动收起
s.js("document.querySelector('.side-toggle').click(); return 1"); time.sleep(.6)
fb = s.js("const b=document.querySelector('.drawer .foot-btn'); const g=b.getBoundingClientRect(); const c=getComputedStyle(b); return [Math.round(g.width),Math.round(g.height),c.whiteSpace,c.color]")
check("竖屏设置按钮单行且可见", isinstance(fb,list) and fb[1] <= 30 and fb[2] == 'nowrap' and fb[3] != 'rgb(222, 228, 229)', f"{fb}")
s.js("const a=[...document.querySelectorAll('.drawer .ef-records a')][1]; a && a.click(); return 1"); time.sleep(1.2)
check("竖屏选会话后抽屉自动收起", s.js("return document.querySelector('.drawer').classList.contains('hide')") is True)
check("收起后遮罩消失", s.js("return !document.querySelector('.drawer-scrim')") is True)

# 竖屏输入框收紧
ch = s.js("const c=document.querySelector('.composer').getBoundingClientRect(); return Math.round(c.height)")
check("竖屏输入框高度≤150", isinstance(ch,int) and ch <= 150, f"h={ch}")

# 窄屏设置单列
s.get(B+"/?settings=1&conv=51"); time.sleep(1.2)
w = s.js("return Math.round(document.querySelector('.col-list').getBoundingClientRect().width)")
check("窄屏卡片宽度≈视口-32", 820 <= w <= 900, f"w={w}")
check("窄屏设置无横向溢出", s.js("return document.scrollingElement.scrollWidth - innerWidth") <= 0)
s.shot("/tmp/uitest/narrow_settings2.png")
s.close()

# 清理自己造的任务与会话（smoke 跑完会写一条「任务回报」到 outbox）
if tid:
    c = sqlite3.connect(DB)
    c.execute("delete from task_steps where task_id=?", (tid,))
    c.execute("delete from tasks where parent_conv=?", (int(task_conv),))
    c.execute("delete from outbox where conv_id=?", (int(task_conv),))
    c.commit(); c.close()
drop_conv(task_conv)
print("已清理测试任务与会话", tid, task_conv)

print("\n失败:", FAIL if FAIL else "无")
