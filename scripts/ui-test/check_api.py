#!/usr/bin/env python3
"""API 契约探测：逐个端点发不同方法，看状态码是否符合 HTTP 语义。

只看两类异常：
  1. 返回 5xx —— 服务端内部错误，不该出现在正常路径上
  2. 返回 2xx 但语义上不该成功（例如 GET 一个只能写的端点）

刻意不发 POST 到 /api/settings、/api/profiles、/api/tools、/api/plugins
这些会改配置的端点 —— 审计不该动用户数据。
"""
import json
import sys
import urllib.request
import urllib.error

BASE = "http://127.0.0.1:18400"

# (路径, 方法, 预期)
CASES = [
    ("/api/health", "GET", 200),
    ("/api/health", "POST", 405),
    ("/api/convs", "GET", 200),
    ("/api/convs", "POST", 405),
    ("/api/convs?archived=1", "GET", 200),
    ("/api/conv/51", "GET", 200),
    ("/api/conv/999999", "GET", 404),
    ("/api/conv/abc", "GET", 404),
    ("/api/conv/-1", "GET", 404),
    ("/api/conv/51", "PUT", 405),
    ("/api/conv/999999", "DELETE", 404),
    ("/api/running", "GET", 200),
    ("/api/context", "GET", 200),
    ("/api/context", "POST", 405),
    ("/api/settings", "GET", 200),
    ("/api/settings", "DELETE", 405),
    ("/api/models", "GET", 200),
    ("/api/models", "DELETE", 405),
    ("/api/tools", "GET", 200),
    ("/api/tools", "DELETE", 405),
    ("/api/profiles", "GET", 200),
    ("/api/profiles", "DELETE", 405),
    ("/api/tasks", "GET", 200),
    ("/api/tasks", "DELETE", 405),
    ("/api/tasks/999999", "GET", 404),
    ("/api/shots/stats", "GET", 200),
    ("/api/shots/stats", "POST", 405),
    ("/api/plugins", "GET", 200),
    ("/api/plugins", "DELETE", 405),
    ("/api/plugins/nope", "GET", 404),
    ("/api/nonexistent", "GET", 404),
]

def probe(path, method):
    req = urllib.request.Request(BASE + path, method=method,
                                 data=b"{}" if method in ("POST", "PUT") else None)
    if method in ("POST", "PUT"):
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=4) as r:
            return r.status, r.read(60).decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read(60).decode("utf-8", "replace")
    except Exception as e:
        return -1, str(e)[:60]

bad = []
for path, method, want in CASES:
    code, body = probe(path, method)
    ok = (code == want)
    mark = "  " if ok else "✗ "
    print(f"{mark}{method:<7} {path:<28} {code:<4} (期望 {want})  {body[:46].replace(chr(10),' ')}")
    if not ok:
        bad.append((method, path, code, want, body[:60]))

print(f"\n=== 不符合预期：{len(bad)} 处 ===")
for m, p, c, w, b in bad:
    print(f"  {m} {p} -> {c}（期望 {w}） {b}")
sys.exit(1 if bad else 0)
