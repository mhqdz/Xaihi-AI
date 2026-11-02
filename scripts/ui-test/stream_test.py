import json, urllib.request, urllib.error, time, threading, sqlite3, sys, os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from dbtool import new_conv as _mkconv, drop_conv

BASE = "http://127.0.0.1:18400"
DB = "/home/mhqdz/.data/agentd/agentd.db"

def post(path, body, timeout=120):
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"}, method="POST")
    return urllib.request.urlopen(req, timeout=timeout)

def get_json(path):
    return json.load(urllib.request.urlopen(BASE + path, timeout=20))

def read_sse(resp, collect=None, stop_after=None):
    """读到 done 或连接结束；返回收到的事件名列表"""
    evs, cur, t0 = [], None, time.time()
    for raw in resp:
        line = raw.decode("utf-8", "replace").rstrip("\n")
        if line.startswith("event: "):
            cur = line[7:]
        elif line.startswith("data: "):
            evs.append(cur or "?")
            if collect is not None and cur in ("delta", "reasoning"):
                collect.append(line[6:])
        if stop_after and time.time() - t0 > stop_after:
            break
    return evs

def db_rows(cid):
    c = sqlite3.connect(DB)
    rows = c.execute("SELECT seq,role,status,length(content),substr(content,1,24) FROM messages WHERE conv_id=? ORDER BY seq", (cid,)).fetchall()
    c.close()
    return rows

def running():
    return get_json("/api/running")

def new_conv(title="stream-test"):
    return _mkconv(title)

results = []
def chk(name, ok, detail=""):
    results.append((name, ok, detail))
    print(("✓ " if ok else "✗ ") + name + ("  " + str(detail) if detail else ""))

# ── 1. 正常发送 → done
cid = new_conv()
print(f"\n[1] 正常发送  conv={cid}")
buf = []
try:
    r = post("/api/chat", {"conv_id": cid, "text": "只回复两个字：好的"})
    evs = read_sse(r, buf)
except Exception as e:
    evs = []; print("  send err:", e)
time.sleep(1.0)
rows = db_rows(cid)
roles = [(x[1], x[2]) for x in rows]
chk("落库 user+assistant", len(rows) >= 2 and roles[0][0] == "user" and roles[-1][0] == "assistant", roles)
chk("assistant status=done", any(x[1] == "assistant" and x[2] == "done" for x in rows), [(x[1], x[2]) for x in rows])
chk("SSE 收到 done", "done" in evs, f"{len(evs)} 帧")

# ── 2. 中途停止 → interrupted
cid2 = new_conv()
print(f"\n[2] 中途停止  conv={cid2}")
holder = {}
def sender():
    try:
        resp = post("/api/chat", {"conv_id": cid2, "text": "写一篇 800 字介绍 Linux 内核调度器"})
        holder["evs"] = read_sse(resp, [])
    except Exception as e:
        holder["err"] = str(e)
th = threading.Thread(target=sender, daemon=True); th.start()
time.sleep(2.5)
chk("停止前在运行列表里", cid2 in running(), running())
try:
    post(f"/api/conv/{cid2}/stop", {}).read()
except Exception as e:
    print("  stop err:", e)
th.join(timeout=25)
time.sleep(1.2)
rows2 = db_rows(cid2)
chk("停止后 status=interrupted", any(x[1] == "assistant" and x[2] == "interrupted" for x in rows2), [(x[1], x[2]) for x in rows2])
chk("中断仍保留正文", any(x[1] == "assistant" and x[3] > 0 for x in rows2), [(x[3], x[4]) for x in rows2 if x[1]=="assistant"])
chk("停止后不再 running", cid2 not in running(), running())

# ── 3. 中断后前端 API 的 kind
print(f"\n[3] kind 判定  conv={cid2}")
d = get_json(f"/api/conv/{cid2}?rounds=5")
kinds = [(m.get("kind"), m.get("status"), m["role"], len(m.get("content") or "")) for m in d["messages"]]
# 规则：有正文 → interrupted；只有“（已停止）”这种说明 → sysnote
body_int = any(k == "interrupted" for k, st, _, n in kinds if st == "interrupted")
note_sys = any(k == "sysnote" for k, st, _, n in kinds if st == "interrupted")
chk("中断归类正确（正文→interrupted / 纯说明→sysnote）", body_int or note_sys,
    [(k, st, n) for k, st, _, n in kinds])

# ── 4. 刷新重连
cid3 = new_conv()
print(f"\n[4] 刷新重连  conv={cid3}")
def sender2():
    try:
        resp = post("/api/chat", {"conv_id": cid3, "text": "写一篇 600 字介绍 TCP 拥塞控制"})
        holder["evs3"] = read_sse(resp, [])
    except Exception as e:
        holder["err3"] = str(e)
th2 = threading.Thread(target=sender2, daemon=True); th2.start()
time.sleep(2.5)
chk("重连前 running 里有它", cid3 in running(), running())
# 模拟刷新：新开一个 /stream 连接读几帧
buf2 = []
try:
    r2 = urllib.request.urlopen(BASE + f"/api/conv/{cid3}/stream", timeout=40)
    evs2 = read_sse(r2, buf2, stop_after=3.0)
    chk("重连接上（收到帧）", len(evs2) > 0, f"{len(evs2)} 帧: {evs2[:5]}")
except Exception as e:
    chk("重连接上（收到帧）", False, str(e))
th2.join(timeout=40)
time.sleep(1.5)
rows3 = db_rows(cid3)
chk("重连后消息完整落库", any(x[1] == "assistant" and x[3] > 0 for x in rows3), [(x[1], x[2], x[3]) for x in rows3])

print("\n=== 汇总 ===")
bad = [r for r in results if not r[1]]
print(f"{len(results)-len(bad)}/{len(results)} 通过")
for n, ok, d in results:
    if not ok: print("  失败:", n, d)
print("测试会话 id:", cid, cid2, cid3)

# 清理：删掉本次造的会话（归档只是 archived=1，仍占列表位）
drop_conv(cid, cid2, cid3)
print("已清理测试会话", cid, cid2, cid3)
