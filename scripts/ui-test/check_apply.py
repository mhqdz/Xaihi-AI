#!/usr/bin/env python3
"""POST /api/apply 的黑盒测试：一次提交多处改动，后端解析分发。

重点不是"能不能存"，而是三条容易被忽略的性质：
  1. 分发正确  —— profile / tool / settings 混在一张表单里也能各归各位
  2. 写前校验  —— 有一项非法时，**排在它前面的合法项也不许落盘**
  3. 错误可定位 —— 告诉调用方是第几项、什么原因，而不是一句"保存失败"

自己改的东西自己还原：只动 tool_rounds 一个数字 + 一个探针 profile。
"""
import json
import sys
import urllib.request
import urllib.error

B = "http://127.0.0.1:18400"
fails = []


def call(method, path, body=None, ctype="application/json"):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(B + path, data=data, method=method)
    if data is not None:
        r.add_header("Content-Type", ctype)
    try:
        with urllib.request.urlopen(r, timeout=10) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw or b"{}")
        except Exception:
            return e.code, {"raw": raw[:80].decode("utf-8", "replace")}
    except Exception as e:
        return -1, {"err": str(e)[:80]}


def check(name, cond, detail=""):
    print(("  ✓ " if cond else "✗ ") + name + (("  " + detail) if detail else ""))
    if not cond:
        fails.append(name)


def settings():
    st, d = call("GET", "/api/settings")
    return d


def profiles():
    st, d = call("GET", "/api/profiles")
    return [p["name"] for p in (d or [])]


base = settings()
orig_rounds = base.get("tool_rounds")
print(f"基线 tool_rounds={orig_rounds}")

# ── 1. 分发：三种 kind 一次提交 ──
st, d = call("POST", "/api/apply", {"changes": [
    {"kind": "settings", "data": {**base, "tool_rounds": (orig_rounds or 50)}},
    {"kind": "profile", "data": {"name": "apply-probe", "base_url": "http://127.0.0.1:18320/180/v1",
                                 "model": "m", "system": "S"}},
]})
check("多类型提交返回 200 且 applied=2", st == 200 and d.get("applied") == 2, f"{st} {d}")
check("profile 真的建出来了", "apply-probe" in profiles())

# ── 2. 写前校验：坏 profile 排在后面，前面的 settings 不许生效 ──
probe_rounds = (orig_rounds or 50) + 7
st, d = call("POST", "/api/apply", {"changes": [
    {"kind": "settings", "data": {**base, "tool_rounds": probe_rounds}},
    {"kind": "profile", "data": {"name": "bad-probe", "model": "m"}},   # 缺 base_url
]})
check("非法项返回 400", st == 400, f"{st}")
check("错误里指明是第 2 项", d.get("index") == 1, f"index={d.get('index')}")
check("错误里带上原因", "base_url" in (d.get("error") or ""), str(d.get("error"))[:50])
check("applied 为 0", d.get("applied") == 0, f"applied={d.get('applied')}")
# 关键：settings 没被写
check("前面的合法项没有落盘（原子性）", settings().get("tool_rounds") == orig_rounds,
      f"现在={settings().get('tool_rounds')} 期望={orig_rounds}")
check("坏 profile 没被创建", "bad-probe" not in profiles())

# ── 3. 错误类型 ──
st, d = call("POST", "/api/apply", {"changes": [{"kind": "bogus", "data": {}}]})
check("未知 kind → 400", st == 400, f"{st} {d.get('error')}")

st, d = call("POST", "/api/apply", {"changes": []})
check("空 changes → 400", st == 400, f"{st}")

st, d = call("POST", "/api/apply", {"changes": [{"kind": "tool", "name": "nope", "data": {"description": "x"}}]})
check("不存在的工具 → 400", st == 400, f"{st} {d.get('error')}")

st, d = call("POST", "/api/apply", {"changes": [{"kind": "settings", "data": base}] * 201})
check("超过 200 项 → 400", st == 400, f"{st}")

# ── 4. 仍然要求 JSON（CSRF 防护没有被新端点绕过）──
st, d = call("POST", "/api/apply", {"changes": []}, ctype="text/plain")
check("text/plain → 415", st == 415, f"{st}")

# ── 清理 ──
call("DELETE", "/api/profiles/apply-probe")
call("PUT", "/api/settings", {**base, "tool_rounds": orig_rounds})
check("探针已清理", "apply-probe" not in profiles())
check("settings 已还原", settings().get("tool_rounds") == orig_rounds,
      f"现在={settings().get('tool_rounds')}")

print(f"\n=== 失败 {len(fails)} 项 ===")
for f in fails:
    print("  ✗ " + f)
sys.exit(1 if fails else 0)
