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
    ("/api/conv/51", "PUT", 405),
    ("/api/conv/999999", "GET", 404),
    ("/api/conv/abc", "GET", 404),
    ("/api/conv/-1", "GET", 404),
    ("/api/conv/999999", "DELETE", 404),
    ("/api/running", "GET", 200),
    # context / models 是先解析参数再判方法的：缺参数时 400 比 405 更准确
    ("/api/context", "GET", 400),
    ("/api/context", "POST", 400),
    ("/api/settings", "GET", 200),
    ("/api/settings", "DELETE", 405),
    ("/api/tools", "GET", 200),
    ("/api/tools", "DELETE", 405),
    ("/api/profiles", "GET", 200),
    ("/api/profiles", "DELETE", 405),
    ("/api/tasks", "GET", 200),
    ("/api/tasks", "DELETE", 405),
    ("/api/tasks/999999", "GET", 404),
    ("/api/shots/stats", "GET", 200),
    ("/api/shots/stats", "POST", 405),
    # 插件列表走 /api/tools；/api/plugins 本身只收 POST/PUT 和 DELETE
    ("/api/plugins", "GET", 405),
    ("/api/plugins", "DELETE", 405),
    ("/api/plugins/nope", "GET", 405),
    # 草稿探测：只收 POST；空 body 是 400（地址为空），不会真去请求上游
    ("/api/endpoints/probe", "GET", 405),
    ("/api/endpoints/probe", "DELETE", 405),
    ("/api/endpoints/probe", "POST", 400),
    ("/api/nonexistent", "GET", 404),
    # CSRF：带 body 的写请求必须 application/json（P0-4）。
    # text/plain 是 CORS 简单请求、不预检，浏览器能跨站发出来 —— 必须 415。
    ("/api/chat", "POST", 415, "text/plain"),
    ("/api/settings", "PUT", 415, "text/plain"),
    ("/api/profiles", "POST", 415, "text/plain"),
    ("/api/plugins", "POST", 415, "text/plain"),
    ("/api/tools/bash", "PUT", 415, "text/plain"),
    ("/api/wake", "POST", 415, "text/plain"),
    ("/api/shots/cleanup", "POST", 415, "text/plain"),
    ("/api/endpoints/probe", "POST", 415, "text/plain"),
    # 目录遍历：%2F 在 path.Clean 之后才解码，曾是整个数据库可下载（P0-3）
    ("/shots/..%2Fagentd.db", "GET", 404),
    ("/shots/..%2Fui.json", "GET", 404),
    ("/shots/..%2F..%2F.ssh%2Fid_rsa", "GET", 404),
]

def probe(path, method, ctype="application/json"):
    req = urllib.request.Request(BASE + path, method=method,
                                 data=b"{}" if method in ("POST", "PUT") else None)
    if method in ("POST", "PUT"):
        req.add_header("Content-Type", ctype)
    try:
        with urllib.request.urlopen(req, timeout=4) as r:
            return r.status, r.read(60).decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read(60).decode("utf-8", "replace")
    except Exception as e:
        return -1, str(e)[:60]

bad = []
for case in CASES:
    path, method, want = case[0], case[1], case[2]
    ctype = case[3] if len(case) > 3 else "application/json"
    code, body = probe(path, method, ctype)
    ok = (code == want)
    mark = "  " if ok else "✗ "
    print(f"{mark}{method:<7} {path:<28} {code:<4} (期望 {want})  {body[:46].replace(chr(10),' ')}")
    if not ok:
        bad.append((method, path, code, want, body[:60]))

print(f"\n=== 不符合预期：{len(bad)} 处 ===")
for m, p, c, w, b in bad:
    print(f"  {m} {p} -> {c}（期望 {w}） {b}")
sys.exit(1 if bad else 0)
