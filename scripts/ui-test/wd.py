import base64, json, os, subprocess, time, urllib.request

BASE = "http://127.0.0.1:4444"

def req(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE + path, data=data, method=method,
                               headers={"Content-Type": "application/json"})
    for _ in range(60):
        try:
            with urllib.request.urlopen(r, timeout=60) as resp:
                return json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            return json.loads(e.read().decode())
        except Exception:
            time.sleep(0.3)
    raise RuntimeError("webdriver 不可用")

def _probe():
    """快速探测 geckodriver 是否 ready（不走 req 的 60 次重试）"""
    try:
        with urllib.request.urlopen(BASE + "/status", timeout=2) as r:
            return json.loads(r.read().decode()).get("value", {}) or {}
    except Exception:
        return {}

def ensure_driver():
    """geckodriver 一旦卡在 'Session already started' 就永远不会自愈，
    而且它没有 /sessions 端点可以清理。唯一可靠做法是杀掉重启。"""
    if _probe().get("ready") is True:
        return
    subprocess.run(["pkill", "-x", "geckodriver"], capture_output=True)
    time.sleep(1.0)
    exe = os.path.expanduser("~/.local/bin/geckodriver")
    subprocess.Popen([exe, "--port", "4444"], stdout=subprocess.DEVNULL,
                     stderr=subprocess.DEVNULL, start_new_session=True)
    for _ in range(24):
        time.sleep(0.5)
        if _probe().get("ready") is True:
            return
    raise RuntimeError("geckodriver 起不来（检查 ~/.local/bin/geckodriver 是否可执行）")

class Session:
    def __init__(self, w=1500, h=950):
        ensure_driver()
        r = None
        for attempt in range(3):
            r = req("POST", "/session", {"capabilities": {"alwaysMatch": {
                "browserName": "firefox",
                "moz:firefoxOptions": {"args": ["-headless", f"--window-size={w},{h}"]}}}})
            if "sessionId" in r.get("value", {}):
                break
            # 还失败：进程彻底卡死，重启一次
            ensure_driver()
            time.sleep(1.0)
        if "sessionId" not in r.get("value", {}):
            raise RuntimeError(f"建会话失败: {r}")
        self.id = r["value"]["sessionId"]
        self.w, self.h = w, h

    def get(self, url):
        return req("POST", f"/session/{self.id}/url", {"url": url})

    def js(self, script, *args):
        r = req("POST", f"/session/{self.id}/execute/sync", {"script": script, "args": list(args)})
        return r.get("value")

    def find(self, css):
        r = req("POST", f"/session/{self.id}/element", {"using": "css selector", "value": css})
        return r.get("value", {}).get("element-6066-11e4-a52e-4f735466cecf")

    def click(self, css):
        eid = self.find(css)
        if not eid:
            return False
        r = req("POST", f"/session/{self.id}/element/{eid}/click", {})
        return "error" not in r

    def text(self, css):
        eid = self.find(css)
        if not eid:
            return None
        return req("GET", f"/session/{self.id}/element/{eid}/text").get("value")

    def shot(self, path):
        r = req("GET", f"/session/{self.id}/screenshot")
        # 目录不存在就建：/tmp 会被系统定期清理，脚本不该因为少个目录就在
        # 最后一步崩掉（崩溃还会跳过清理，把测试会话留在真实库里）
        d = os.path.dirname(os.path.abspath(path))
        if d:
            os.makedirs(d, exist_ok=True)
        with open(path, "wb") as f:
            f.write(base64.b64decode(r["value"]))
        return path

    def rect(self):
        return req("GET", f"/session/{self.id}/window/rect").get("value")

    def close(self):
        req("DELETE", f"/session/{self.id}")
