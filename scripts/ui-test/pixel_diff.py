#!/usr/bin/env python3
"""像素基线对比：同一组页面/尺寸各截一张，比较改前改后是否完全一致。
用法：
  python3 pixel_diff.py baseline <dir>
  python3 pixel_diff.py compare  <dir>
"""
import sys, os, time, hashlib
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req
from PIL import Image, ImageChops

BASE = "http://127.0.0.1:18400"
SHOTS = [
    ("wide-conv51", 1600, 1000, "/?conv=51"),
    ("wide-conv118", 1600, 1000, "/?conv=118"),
    ("wide-empty", 1600, 1000, "/"),
    ("wide-settings", 1600, 1000, "/?settings=1&conv=51"),
    ("narrow-conv51", 900, 1000, "/?conv=51"),
    ("narrow-empty", 900, 1000, "/"),
]

def grab(name, w, h, path, outdir):
    s = Session(w, h)
    req("POST", f"/session/{s.id}/window/rect", {"width": w, "height": h})
    s.get(BASE + path)
    time.sleep(2.2)
    p = os.path.join(outdir, name + ".png")
    s.shot(p)
    s.close()
    return p

def main(mode, outdir):
    os.makedirs(outdir, exist_ok=True)
    if mode == "baseline":
        for name, w, h, path in SHOTS:
            p = grab(name, w, h, path, outdir)
            print("  saved", p)
        return 0
    bad = []
    for name, w, h, path in SHOTS:
        cur = os.path.join("/tmp/pixeltmp", name + ".png")
        os.makedirs("/tmp/pixeltmp", exist_ok=True)
        grab(name, w, h, path, "/tmp/pixeltmp")
        ref = os.path.join(outdir, name + ".png")
        a = Image.open(ref).convert("RGB"); b = Image.open(cur).convert("RGB")
        if a.size != b.size:
            bad.append((name, f"尺寸不同 {a.size} vs {b.size}")); continue
        diff = ImageChops.difference(a, b)
        bbox = diff.getbbox()
        if bbox:
            px = sum(1 for p in diff.getdata() if p != (0, 0, 0))
            bad.append((name, f"{px} 像素不同，区域 {bbox}"))
        else:
            print("  ✓", name)
    if bad:
        print("\n✗ 有差异:")
        for n, d in bad:
            print("  ", n, d)
        return 1
    print("\n✓ 全部一致")
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1], sys.argv[2]))
