#!/usr/bin/env python3
"""右侧标尺（.scale）在任何轮次规模下都不能溢出视口。

它曾经用固定 gap:12px 渲染 total_rounds 条刻度 —— 41 轮的会话稳稳装得下，
但 200 轮时总高 2789px、上下各溢出 937px，大半刻度被裁到屏幕外，
标尺从「点任意一轮」退化成「只能点中间一小段」。
"""
import os
import sqlite3
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req
from dbtool import new_conv, drop_conv

B = "http://127.0.0.1:18400"
DB = os.path.expanduser("~/.data/agentd/agentd.db")
FAIL = []


def check(name, cond, extra=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (f"  {extra}" if extra else ""))
    if not cond:
        FAIL.append(name)


def seed(cid, rounds):
    c = sqlite3.connect(DB)
    now = "2026-11-03T07:00:00+08:00"
    seq = 0
    for i in range(rounds):
        seq += 1
        c.execute("insert into messages(conv_id,seq,role,content,status,origin,created_at) values(?,?,?,?,?,?,?)",
                  (cid, seq, "user", f"问{i+1}", "done", "user", now))
        seq += 1
        c.execute("insert into messages(conv_id,seq,role,content,status,origin,created_at) values(?,?,?,?,?,?,?)",
                  (cid, seq, "assistant", f"答{i+1}", "done", "user", now))
    c.commit()
    c.close()


PROBE = '''
const sc = document.querySelector('.scale');
if (!sc) return {err: 'no .scale'};
const r = sc.getBoundingClientRect();
const cs = getComputedStyle(sc);
return {
  marks: sc.children.length,
  h: Math.round(r.height),
  top: Math.round(r.top),
  bottom: Math.round(r.bottom),
  winH: innerHeight,
  gap: cs.rowGap,
  overTop: Math.round(Math.max(0, -r.top)),
  overBottom: Math.round(Math.max(0, r.bottom - innerHeight)),
};
'''


def probe(s, cid):
    s.get(f"{B}/?conv={cid}")
    for _ in range(40):
        time.sleep(0.25)
        v = s.js(PROBE)
        if isinstance(v, dict) and v.get("marks"):
            return v
    return s.js(PROBE)


big = new_conv("ui-scale-200")
seed(big, 200)
s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
try:
    v = probe(s, big)
    check("200 轮：渲染了刻度", isinstance(v, dict) and v.get("marks", 0) >= 100, v.get("marks"))
    check("200 轮：总高不超过视口", v["h"] <= v["winH"], f'{v["h"]} <= {v["winH"]}')
    check("200 轮：顶部不溢出", v["overTop"] == 0, v["overTop"])
    check("200 轮：底部不溢出", v["overBottom"] == 0, v["overBottom"])
    check("200 轮：间距被压缩到 12px 以下", float(v["gap"].rstrip("px")) < 12, v["gap"])

    v = probe(s, 51)
    check("41 轮：间距保持 12px（视觉不变）", v["gap"] == "12px", v["gap"])
    check("41 轮：不溢出", v["overTop"] == 0 and v["overBottom"] == 0, f'{v["overTop"]}/{v["overBottom"]}')
finally:
    s.close()
    drop_conv(big)
    print("已清理测试会话")

print("\n失败:", FAIL if FAIL else "无")
sys.exit(1 if FAIL else 0)
