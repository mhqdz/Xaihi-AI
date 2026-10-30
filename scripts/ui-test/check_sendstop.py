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

OBSERVE = """
window.__btn = [];
const box = document.querySelector('.composer .row3');
const snap = () => {
  const b = document.querySelector('.composer .send');
  if (!b) return;
  const s = (b.className || '').replace('send','send').trim() + (b.disabled ? ':disabled' : ':enabled');
  if (window.__btn[window.__btn.length - 1] !== s) window.__btn.push(s);
};
snap();
window.__obs = new MutationObserver(snap);
window.__obs.observe(box, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'disabled'] });
return 1
"""

def type_text(s, t):
    s.js("""const ta=document.querySelector('.composer textarea');
const set=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;
set.call(ta, """ + json.dumps(t) + """);
ta.dispatchEvent(new Event('input',{bubbles:true})); return ta.value""")

def wait_idle(s, limit=180):
    t0 = time.time()
    while time.time() - t0 < limit:
        st = s.js("const b=document.querySelector('.composer .send'); return b? b.className + (b.disabled?'1':'0') : ''")
        if st and 'stop' not in st and 'interject' not in st and 'loading' not in st:
            return True, time.time() - t0
        time.sleep(0.2)
    return False, time.time() - t0

s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
s.get(BASE + "/")
time.sleep(1.8)

s.js("""const sel=document.querySelector('.composer select');
const set=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set;
set.call(sel,'180-low'); sel.dispatchEvent(new Event('change',{bubbles:true})); return sel.value""")
time.sleep(0.2)
s.js(OBSERVE)

# ─────────── 发送：点击瞬间必须是"转圈 + disabled" ───────────
type_text(s, '只回复两个字：收到。不要调用工具。')
time.sleep(0.4)
s.js("document.querySelector('.composer .send').click(); return 1")
time.sleep(0.05)
seq_a = s.js("return window.__btn.slice()")
print("  发送序列:", seq_a)
check("点发送后立即进入 loading（不可点）", any('loading' in x and 'disabled' in x for x in seq_a), str(seq_a[:3]))
i_load = next((i for i, x in enumerate(seq_a) if 'loading' in x), 99)
i_stop = next((i for i, x in enumerate(seq_a) if 'stop' in x), 99)
check("loading 出现在 stop 之前", i_load < i_stop, f"load@{i_load} stop@{i_stop}")
ok, secs = wait_idle(s)
check("生成结束回到普通发送键", ok, f"{secs:.1f}s")
seq_a = s.js("return window.__btn.slice()")
print("  完整序列:", seq_a)
check("发送 → 中止 → 结束 三态齐全", any('loading' in x for x in seq_a) and any('stop' in x for x in seq_a) and seq_a[-1].startswith('send'), str(seq_a))

cid = s.js("return new URLSearchParams(location.search).get('conv')")
users = [m for m in api('/api/conv/' + str(cid))['messages'] if m['role'] == 'user']
check("只发出去一条（防重复提交）", len(users) == 1, f"user messages={len(users)}")

# ─────────── 中止：点击后到收尾前是"转圈 + disabled" ───────────
s.js("window.__btn = []; return 1")
type_text(s, '写一段 800 字的说明文字，分段写，不要调用工具。')
time.sleep(0.3)
s.js("document.querySelector('.composer .send').click(); return 1")
# 等到生成中（按钮变 stop）
t0 = time.time()
while time.time() - t0 < 30:
    st = s.js("const b=document.querySelector('.composer .send'); return b? b.className : ''")
    if 'stop' in st:
        break
    time.sleep(0.2)
check("进入生成态（按钮是中止）", 'stop' in (s.js("return document.querySelector('.composer .send').className") or ''))
spinner_before = s.js("return document.querySelectorAll('.composer .send.loading').length")
s.js("document.querySelector('.composer .send').click(); return 1")
check("中止前按钮不是 loading（对比）", spinner_before == 0, str(spinner_before))
ok, secs = wait_idle(s, 30)
check("中止收尾后回到普通键", ok, f"{secs:.1f}s")
seq_b = s.js("return window.__btn.slice()")
print("  中止序列:", seq_b)
i_stop = next((i for i, x in enumerate(seq_b) if 'stop' in x), 99)
i_last_load = max((i for i, x in enumerate(seq_b) if 'loading' in x), default=-1)
check("中止过场有序：stop → loading(disabled) → 普通键",
      i_last_load > i_stop and 'disabled' in seq_b[i_last_load] and seq_b[-1].startswith('send'),
      f"stop@{i_stop} lastLoad@{i_last_load}={seq_b[i_last_load]} last={seq_b[-1]}")
check("收尾后状态是已停止", '已停止' in (s.text('.status') or ''), s.text('.status'))

# 清理：把测试会话归档
s.close()
try:
    u = urllib.request.Request(BASE + "/api/conv/" + str(cid), method="DELETE")
    urllib.request.urlopen(u, timeout=10).read()
    print("已归档测试会话", cid)
except Exception as e:
    print("归档失败", e)

print("失败:", "、".join(FAIL) if FAIL else "无")
