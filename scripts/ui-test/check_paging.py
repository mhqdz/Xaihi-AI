#!/usr/bin/env python3
"""分页窗口的边界行为：会话很短 / 空 / 参数越界时不该算出负数或错位的 from/to。

这些值直接决定前端标尺画几格、上翻还能不能再加载，算错就是"状态错乱"。
以前只在 575 条消息的 conv51 上测过 —— 那种规模下所有 clamp 都不会触发。
"""
import json
import os
import sqlite3
import sys
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from dbtool import new_conv, drop_conv

B = "http://127.0.0.1:18400"
DB = os.path.expanduser("~/.data/agentd/agentd.db")
FAIL = []


def check(name, cond, extra=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (f"  {extra}" if extra else ""))
    if not cond:
        FAIL.append(name)


def api(path):
    with urllib.request.urlopen(B + path, timeout=15) as r:
        return json.loads(r.read())


def seed(cid, n_msgs):
    c = sqlite3.connect(DB)
    now = "2026-11-03T07:00:00+08:00"
    for i in range(1, n_msgs + 1):
        role = "user" if i % 2 == 1 else "assistant"
        c.execute(
            "insert into messages(conv_id,seq,role,content,status,origin,created_at) values(?,?,?,?,?,?,?)",
            (cid, i, role, f"第{i}条", "done", "user", now))
    c.commit()
    c.close()


def page(cid, q=""):
    return api(f"/api/conv/{cid}?rounds=10{q}").get("page", {})


def sane(p):
    """窗口的几个不变量：非负、from<=to、rounds 内、remaining 与 total 自洽。"""
    ok = True
    ok &= p["from"] >= 0 and p["to"] >= 0
    ok &= p["from"] <= p["to"] or p["from"] == 0
    ok &= p["to"] <= p["total_rounds"] or p["total_rounds"] == 0
    ok &= p["remaining_rounds"] >= 0
    ok &= p["remaining_rounds"] + (p["to"] - p["from"] + 1 if p["to"] else 0) == p["total_rounds"] or p["total_rounds"] == 0
    ok &= p["to"] - p["from"] + 1 <= 10 if p["to"] else True
    return ok


empty = new_conv("ui-pg-empty")
one = new_conv("ui-pg-one")       # 2 条 = 1 轮
three = new_conv("ui-pg-three")   # 3 条 = 2 轮
seed(one, 2)
seed(three, 3)

try:
    p = page(empty)
    check("空会话 from/to 均为 0", p["from"] == 0 and p["to"] == 0 and p["messages"] == 0, p)
    check("空会话 total_rounds=0", p["total_rounds"] == 0, p["total_rounds"])
    check("空会话不变量", sane(p))

    p = page(one)
    check("1 轮会话 from=1 to=1", p["from"] == 1 and p["to"] == 1, p)
    check("1 轮会话没有更多", p["has_more"] is False, p["has_more"])
    check("1 轮会话不变量", sane(p))

    p = page(three)
    check("3 条消息算出 2 轮", p["total_rounds"] == 2, p["total_rounds"])
    check("3 条消息全量返回", p["from"] == 1 and p["to"] == 2 and p["messages"] == 3, p)
    check("3 条消息不变量", sane(p))

    # 参数越界
    p = page(three, "&before=1")
    check("before 小于最小 seq 返回空窗口", p["messages"] == 0 and p["from"] == 0 and p["to"] == 0, p)
    check("before=1 没有更多", p["has_more"] is False, p["has_more"])

    p = page(three, "&around=99")
    check("around 超过总轮数被夹回", p["from"] >= 1 and p["to"] == p["total_rounds"], p)
    check("around=99 不变量", sane(p))

    p = page(three, "&around=0")
    check("around=0 当作未指定", sane(p), p)

    p = api(f"/api/conv/{three}?rounds=0")["page"]
    check("rounds=0 被当作默认 10", p["rounds"] == 10, p["rounds"])
    p = api(f"/api/conv/{three}?rounds=-5")["page"]
    check("rounds=-5 不产生负窗口", sane(p), p)
finally:
    for cid in (empty, one, three):
        drop_conv(cid)
    print("已清理测试会话")

print("\n失败:", FAIL if FAIL else "无")
sys.exit(1 if FAIL else 0)
