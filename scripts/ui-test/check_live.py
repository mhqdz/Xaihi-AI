import sys, os, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req
from dbtool import drop_conv

BASE = "http://127.0.0.1:18400"
FAIL = []
def check(name, cond, extra=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (f"  {extra}" if extra else ""))
    if not cond: FAIL.append(name)

s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
s.get(BASE + "/")
time.sleep(1.5)

s.click('.new-conv')
time.sleep(0.8)
sel = s.js("return document.querySelectorAll('.composer select').length")
check("新建会话后输入区可用", sel == 1, f"n={sel}")

prof = s.js("""const sel=document.querySelector('.composer select');
const set=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set;
set.call(sel,'180-low'); sel.dispatchEvent(new Event('change',{bubbles:true})); return sel.value""")
check("切换到 180-low", prof == '180-low', str(prof))
time.sleep(0.3)

s.js("window.__renders={msg:0,tool:0,note:0}; return 1")
s.js("""const ta=document.querySelector('.composer textarea');
const set=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;
set.call(ta,'只回复两个字：收到。不要调用工具。'); ta.dispatchEvent(new Event('input',{bubbles:true})); return ta.value""")
time.sleep(0.5)
check("有文字后发送按钮可用", s.js("return !!document.querySelector('.composer .send:not([disabled])')") == True)
s.click('.composer .send')

t0 = time.time()
done = False
live_seen = False
while time.time() - t0 < 240:
    st = s.js("""const b=document.querySelector('.composer .send');
return {cls: b? b.className:'', live: !!document.querySelector('.msg.assistant .prose') && !!document.querySelector('.live-wait, .msg.assistant'),
        nmsg: document.querySelectorAll('.msg.assistant').length,
        tail: (document.querySelector('.msg.assistant .md')||{}).innerText||''}""")
    if 'stop' in st['cls'] or 'interject' in st['cls']:
        live_seen = True
    else:
        done = True
        break
    time.sleep(1.0)

check("生成完成（按钮回到发送态）", done, f"耗时 {time.time()-t0:.1f}s")
time.sleep(1.0)
n = s.js("return document.querySelectorAll('.msg.assistant').length")
tags = s.js("return [...document.querySelectorAll('.msg.assistant .ef-tag')].map(e=>e.textContent)")
body = s.js("return (document.querySelector('.msg.assistant .md')||{}).innerText||''")
check("落库后是正式 Agent 卡（不是实时气泡）", n >= 1, f"cards={n}")
check("Agent 卡带 No. 编号", any(t.startswith('No.') for t in tags), str(tags))
check("正文已渲染 markdown", len(body.strip()) > 0, repr(body[:40]))
check("没有残留的\"生成中…\"气泡", s.js("return [...document.querySelectorAll('.msg')].filter(e=>e.innerText.includes('生成中…')).length") == 0)
check("回复只有一张 Agent 卡", s.js("return [...document.querySelectorAll('.msg.assistant .md')].filter(e=>e.innerText.trim()==='收到').length") == 1)
r = s.js("return window.__renders")
check("追加消息时历史卡不重渲染（memo）", r['msg'] <= 4, str(r))

errs = s.js("return (window.__errs||[]).slice(0,3)")
check("无 JS 错误", not errs, str(errs))
s.shot("/tmp/uitest/step2_live.png")
print("失败:", "、".join(FAIL) if FAIL else "无")
# 收尾：删掉这个测试会话（归档只是 archived=1，仍然占着会话列表的位置）
cid = s.js("return new URLSearchParams(location.search).get('conv')")
s.close()
drop_conv(cid)
print("已清理测试会话", cid)
