import base64, json, time, urllib.request

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

def clear_sessions():
    """清掉 geckodriver 里残留的会话（DELETE /session 无 id 不是标准，必须按 id 删）"""
    try:
        r = req("GET", "/sessions")
        for s in (r.get("value") or []):
            sid = s.get("id")
            if sid:
                try:
                    req("DELETE", "/session/" + sid)
                except Exception:
                    pass
    except Exception:
        pass

class Session:
    def __init__(self, w=1500, h=950):
        r = None
        for attempt in range(3):
            r = req("POST", "/session", {"capabilities": {"alwaysMatch": {
                "browserName": "firefox",
                "moz:firefoxOptions": {"args": ["-headless", f"--window-size={w},{h}"]}}}})
            if "sessionId" in r.get("value", {}):
                break
            # 上一个用例没收尾：按 id 清掉再试
            clear_sessions()
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
        with open(path, "wb") as f:
            f.write(base64.b64decode(r["value"]))
        return path

    def rect(self):
        return req("GET", f"/session/{self.id}/window/rect").get("value")

    def close(self):
        req("DELETE", f"/session/{self.id}")
