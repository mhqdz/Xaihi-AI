import sys, os, time, json, sqlite3, urllib.request
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req
from dbtool import new_conv, drop_conv

DB = os.path.expanduser("~/.data/agentd/agentd.db")

FAIL = []
def check(name, cond, extra=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (f"  {extra}" if extra else ""))
    if not cond: FAIL.append(name)

def api(path):
    with urllib.request.urlopen("http://127.0.0.1:18400" + path, timeout=30) as r:
        return json.loads(r.read())

BASE = "http://127.0.0.1:18400"
# 准备一条任务（有步骤可看）
# 只能清理自己造的那份：库里已有任务时别动别人的
created_cid, created_tid = None, None
import atexit

def _cleanup_run():
    if created_cid:
        drop_conv(created_cid)
atexit.register(_cleanup_run)   # 中途崩了也要清，否则测试会话会留在真实库里

tasks = api("/api/tasks").get("tasks") or []
if not tasks:
    created_cid = new_conv("ui-run")
    r = urllib.request.Request(BASE + "/api/tasks",
        data=json.dumps({"kind": "smoke", "conv_id": created_cid, "card": {"goal": "ui"}}).encode(),
        headers={"Content-Type": "application/json"}, method="POST")
    created_tid = json.loads(urllib.request.urlopen(r, timeout=30).read()).get("task_id")
    for _ in range(24):
        time.sleep(0.5)
        d = json.loads(urllib.request.urlopen(BASE + "/api/tasks/" + str(created_tid), timeout=10).read())
        if (d.get("task") or {}).get("steps") or d.get("steps"):
            break
    time.sleep(1.5)
    tasks = api("/api/tasks")["tasks"]
TID = tasks[0]["id"]

def wide():
    s = Session(1600, 1000)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
    s.get(BASE + "/?conv=51"); time.sleep(1.5)
    check("宽屏无横向溢出", s.js("return document.scrollingElement.scrollWidth - window.innerWidth") <= 0)
    check("右栏存在且宽 64", (s.js("const r=document.querySelector('.rail'); return r? r.getBoundingClientRect().width : -1") or -1) == 64)

    # 思考块
    n = s.js("return document.querySelectorAll('details.fold').length")
    check("有思考块", n >= 1, f"n={n}")
    if n:
        s.js("const d=document.querySelector('details.fold'); d.scrollIntoView({block:'center'}); return d.open")
        s.click("details.fold > summary"); time.sleep(.4)
        check("展开后 details.open", s.js("return document.querySelector('details.fold').open") is True)
        cap = s.js("const p=document.querySelector('details.fold pre.think'); return p? (p.scrollHeight - p.clientHeight) : -1")
        check("思考正文不再内部滚动（无高度上限）", cap is not None and cap <= 2, f"overflow={cap}")
        check("底部有收起条", s.js("const b=document.querySelector('details.fold .fold-close'); return !!b && b.getBoundingClientRect().height>0") is True)
        s.shot("/tmp/uitest/wide_think.png")
        s.click("details.fold .fold-close"); time.sleep(.5)
        check("点收起条后关闭", s.js("return document.querySelector('details.fold').open") is False)
        off = s.js("const d=document.querySelector('details.fold'); const m=d.closest('.msg'); const t=document.querySelector('.thread')||document.scrollingElement; return m.getBoundingClientRect().top - t.getBoundingClientRect().top")
        check("收起后滚到该消息开头附近", off is not None and -60 <= off <= 120, f"off={off}")

    # 设置：宽屏三级
    s.get(BASE + "/?settings=1&conv=51"); time.sleep(1.2)
    check("窄屏外：列表卡可见", s.js("const e=document.querySelector('.col-list'); return e && getComputedStyle(e).display!=='none'") is True)
    s.js("const items=[...document.querySelectorAll('.col-list .item.col')]; items[0].click(); return true"); time.sleep(.6)
    check("点模型 → 详情卡出现", s.js("const e=document.querySelector('.col-detail'); return e && getComputedStyle(e).display!=='none'") is True)
    check("详情卡在 .set-cols 内（与列表同容器）", s.js("const f=document.querySelector('.col-detail'); return f && f.parentElement.classList.contains('set-cols')") is True)
    check("详情头部是 ef-label 样式", s.js("const h=document.querySelector('.col-detail .card-hd'); return h && !!h.querySelector('.ef-label')") is True)
    r = s.js("const f=document.querySelector('.col-detail').getBoundingClientRect(); const l=document.querySelector('.col-list').getBoundingClientRect(); return [f.width,l.width, Math.round(innerWidth-l.right)]")
    check("两级卡片宽度一致（同档）", abs(r[0]-r[1]) <= 1, f"{r}")
    check("列表右缘贴视口（≤24px）", 0 <= r[2] <= 24, f"gap={r[2]}")
    s.shot("/tmp/uitest/wide_model.png")

    # 工具卡：互斥 + 表单
    s.js("const t=[...document.querySelectorAll('.set-card .item')].find(x=>x.textContent.startsWith('kb_get')); t.click(); return true"); time.sleep(.6)
    check("点插件 → 工具卡出现", s.js("const e=document.querySelector('.col-detail'); return e && getComputedStyle(e).display!=='none'") is True)
    check("工具卡与模型详情互斥（只有一张 2 级卡）", s.js("return document.querySelectorAll('.col-detail').length") == 1)
    check("工具表单字段齐全(≥5)", s.js("return document.querySelectorAll('.col-detail .field').length") >= 5,
          f"n={s.js('return document.querySelectorAll('+chr(34)+'.col-detail .field'+chr(34)+').length')}")
    check("DOM 顺序：工具卡在列表之前", s.js("const t=document.querySelector('.col-detail'), l=document.querySelector('.col-list'); return !!(t && l && (t.compareDocumentPosition(l) & Node.DOCUMENT_POSITION_FOLLOWING))") is True)
    s.shot("/tmp/uitest/wide_tool.png")

    # 任务详情
    s.get(BASE + f"/?settings=1&conv=51&task={TID}"); time.sleep(1.5)
    steps = s.js("return document.querySelectorAll('.task-step').length")
    check("任务详情渲染步骤", steps >= 1, f"steps={steps}")
    s.shot("/tmp/uitest/wide_task.png")
    check("宽屏无横向溢出（设置页）", s.js("return document.scrollingElement.scrollWidth - window.innerWidth") <= 0)
    s.close()

def narrow():
    s = Session(900, 1300)
    req("POST", f"/session/{s.id}/window/rect", {"width": 900, "height": 1300})
    s.get(BASE + "/?conv=51"); time.sleep(1.5)
    check("窄屏无横向溢出", s.js("return document.scrollingElement.scrollWidth - innerWidth") <= 0)
    s.get(BASE + "/?settings=1&conv=51"); time.sleep(1.2)
    vis = s.js("return [...document.querySelectorAll('.set-cols > .card')].map(c=>[c.className.replace('card ',''), getComputedStyle(c).display!=='none'])")
    check("窄屏只显示一级卡", sum(1 for v in vis if v[1]) == 1 and any(v[0].startswith('col-list') and v[1] for v in vis), f"{vis}")
    s.js("document.querySelector('.col-list .item.col').click(); return true"); time.sleep(.5)
    check("点模型 → 详情独占一屏", s.js("const f=document.querySelector('.col-detail'); const l=document.querySelector('.col-list'); return getComputedStyle(f).display!=='none' && getComputedStyle(l).display==='none'") is True)
    check("窄屏有 × 返回", s.js("const b=document.querySelector('.col-detail .card-back'); return !!b && b.getBoundingClientRect().width>0") is True)
    s.shot("/tmp/uitest/narrow_model.png")
    s.js("const it=[...document.querySelectorAll('.col-detail .item')].find(x=>x.textContent.trim().startsWith('system')); it.click(); return true"); time.sleep(.6)
    check("点 system → 三级编辑卡", s.js("const e=document.querySelector('.col-editor'); return e && getComputedStyle(e).display!=='none'") is True)
    check("编辑卡有内容", (s.js("const t=document.querySelector('.col-editor textarea'); return t? t.value.length : -1") or 0) > 100)
    s.shot("/tmp/uitest/narrow_editor.png")
    s.js("document.querySelector('.col-editor .card-back').click(); return true"); time.sleep(.5)
    check("× 返回二级", s.js("const e=document.querySelector('.col-editor'); return !e || getComputedStyle(e).display==='none'") is True)
    s.close()

def composer():
    s = Session(1200, 900)
    req("POST", f"/session/{s.id}/window/rect", {"width": 1200, "height": 900})
    s.get(BASE + "/?conv=51"); time.sleep(1.2)
    check("发送按钮初始禁用", s.js("const b=document.querySelector('button.send'); return b ? b.disabled : null") is True)
    s.js("const t=document.querySelector('textarea'); t.focus(); return true")
    req("POST", f"/session/{s.id}/element/" + s.find("textarea") + "/value", {"text": "hello"})
    time.sleep(.3)
    check("有文字后发送可用", s.js("const b=document.querySelector('button.send'); return b ? !b.disabled : null") is True)
    s.shot("/tmp/uitest/composer_ready.png")
    s.close()

wide(); narrow(); composer()

# 清理自己造的那份（smoke 任务跑完还会往 outbox 写一条「任务回报」）
if created_cid:
    c = sqlite3.connect(DB)
    c.execute("delete from task_steps where task_id=?", (created_tid,))
    c.execute("delete from tasks where parent_conv=?", (int(created_cid),))
    c.execute("delete from outbox where conv_id=?", (int(created_cid),))
    c.commit(); c.close()
    drop_conv(created_cid)
    print("已清理测试任务与会话", created_tid, created_cid)

print("\n失败:", FAIL if FAIL else "无")
