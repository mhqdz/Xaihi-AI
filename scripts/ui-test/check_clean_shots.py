#!/usr/bin/env python3
"""「立即清理」按钮：会真的删文件，只能在隔离实例上验。

自己起一个 AGENTD_DATA_DIR 独立的实例（主实例的截图一张都不碰），
造三个不同年龄的 .png，点按钮，确认只删掉超过保留天数的。

这是配置面板里唯一一个"按一下会改磁盘"的控件，所以单独一个脚本，
不塞进 check_config（那个打的是真实实例）。
"""
import json
import os
import shutil
import subprocess
import sys
import time
import urllib.request

sys.path.insert(0, "scripts/ui-test")
from wd import Session, req

PORT = 18514
DIR = "/tmp/agentd-shotstest"
BIN = os.path.expanduser("~/.local/bin/agentd")
WEB = os.path.expanduser("~/Workspace/agentd/web/dist")
B = f"http://127.0.0.1:{PORT}"
fails = []


def check(name, cond, detail=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (("  " + str(detail)) if detail else ""))
    if not cond:
        fails.append(name)


def api(path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(B + path, data=data, method="POST" if data else "GET")
    if data is not None:
        r.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(r, timeout=10) as resp:
        return json.loads(resp.read() or b"{}")


def wait_up(timeout=15):
    for _ in range(timeout * 4):
        try:
            api("/api/settings")
            return True
        except Exception:
            time.sleep(0.25)
    return False


def main():
    shutil.rmtree(DIR, ignore_errors=True)
    os.makedirs(os.path.join(DIR, "shots"), exist_ok=True)
    with open(os.path.join(DIR, "profiles.json"), "w") as f:
        json.dump([{"name": "p", "base_url": "http://127.0.0.1:18320/180/v1", "model": "m"}], f)

    shots = os.path.join(DIR, "shots")
    for name, age in (("old1.png", "3 days ago"), ("old2.png", "2 days ago"), ("fresh.png", "1 hour ago")):
        p = os.path.join(shots, name)
        with open(p, "wb") as f:
            f.write(b"PNG")
        subprocess.run(["touch", "-d", age, p], check=True)

    env = dict(os.environ, AGENTD_ADDR=f"127.0.0.1:{PORT}", AGENTD_DATA_DIR=DIR,
               AGENTD_PROFILES=os.path.join(DIR, "profiles.json"), AGENTD_WEB_DIR=WEB)
    proc = subprocess.Popen([BIN, "serve"], env=env,
                            stdout=open("/tmp/agentd-shotstest.log", "wb"), stderr=subprocess.STDOUT)
    s = None
    try:
        if not wait_up():
            print("  ✗ 隔离实例没起来，见 /tmp/agentd-shotstest.log")
            return 1
        check("隔离实例起来", True)
        check("初始有 3 张", len(os.listdir(shots)) == 3, os.listdir(shots))

        # 通过 API 设保留天数，避免为了改一个数字而多点一次 UI（这一步不触发清理：
        # 值没变；真正的清理由下面那个按钮触发）
        api("/api/settings", {"tool_rounds_enabled": True, "tool_rounds": 50,
                              "auto_archive_enabled": True, "auto_archive_days": 30,
                              "shots_auto_clean": True, "shots_keep_days": 1})
        time.sleep(0.5)
        remaining = sorted(os.listdir(shots))
        check("改设置本身就会清理一次旧的", remaining == ["fresh.png"], remaining)

        # 再把两个旧的造回来，专门测按钮
        for name in ("old1.png", "old2.png"):
            p = os.path.join(shots, name)
            with open(p, "wb") as f:
                f.write(b"PNG")
            subprocess.run(["touch", "-d", "3 days ago", p], check=True)
        check("重置为 3 张", len(os.listdir(shots)) == 3, sorted(os.listdir(shots)))

        s = Session(1600, 1000)
        req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
        s.get(B + "/?settings=1")
        time.sleep(2.5)
        btn = s.js("""const b=[...document.querySelectorAll('button')].find(x=>x.textContent.trim()==='立即清理');
          if(!b) return null;
          const r=b.getBoundingClientRect();
          return {w:Math.round(r.width), h:Math.round(r.height), type:b.type};""")
        check("按钮存在且够大", isinstance(btn, dict) and btn["h"] >= 24 and btn["w"] >= 24, btn)
        check("按钮不是 submit", isinstance(btn, dict) and btn["type"] == "button", (btn or {}).get("type"))

        before = s.js("""const m=[...document.querySelectorAll('.col-list .item-desc')].find(e=>/当前.*张/.test(e.textContent));
          return m ? m.textContent.trim() : null;""")
        check("点前显示张数", isinstance(before, str) and "3 张" in before, before)

        s.js("const b=[...document.querySelectorAll('button')].find(x=>x.textContent.trim()==='立即清理'); b&&b.click(); return !!b")
        time.sleep(2.0)

        after = sorted(os.listdir(shots))
        check("按钮删掉了旧的", after == ["fresh.png"], after)
        text = s.js("""const m=[...document.querySelectorAll('.col-list .item-desc')].find(e=>/当前.*张/.test(e.textContent));
          return m ? m.textContent.trim() : null;""")
        check("统计跟着更新", isinstance(text, str) and "1 张" in text, text)
    finally:
        if s:
            s.close()
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except Exception:
            proc.kill()
        shutil.rmtree(DIR, ignore_errors=True)

    print(f"\n=== 失败 {len(fails)} 项 ===")
    for f in fails:
        print("  ✗ " + f)
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
