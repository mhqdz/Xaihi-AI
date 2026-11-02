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
# (名字, 宽, 高, 路径, 左侧裁掉多少 px)
# 左栏是会话列表，条目随测试数据增减而变化，跟样式无关 —— 不纳入对比，
# 否则每跑一次测试就会误报整列差异。宽屏抽屉 260px、窄屏 250px，多裁 2px 边框。
SHOTS = [
    ("wide-conv51", 1600, 1000, "/?conv=51", 262),
    ("wide-conv118", 1600, 1000, "/?conv=118", 262),
    ("wide-empty", 1600, 1000, "/", 262),
    ("wide-settings", 1600, 1000, "/?settings=1&conv=51", 262),
    ("narrow-conv51", 900, 1000, "/?conv=51", 252),
    ("narrow-empty", 900, 1000, "/", 252),
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
        for name, w, h, path, _ in SHOTS:
            p = grab(name, w, h, path, outdir)
            print("  saved", p)
        return 0
    bad = []
    for name, w, h, path, crop in SHOTS:
        cur = os.path.join("/tmp/pixeltmp", name + ".png")
        os.makedirs("/tmp/pixeltmp", exist_ok=True)
        grab(name, w, h, path, "/tmp/pixeltmp")
        ref = os.path.join(outdir, name + ".png")
        a = Image.open(ref).convert("RGB"); b = Image.open(cur).convert("RGB")
        if a.size != b.size:
            bad.append((name, f"尺寸不同 {a.size} vs {b.size}")); continue
        a = a.crop((crop, 0, a.width, a.height))
        b = b.crop((crop, 0, b.width, b.height))
        diff = ImageChops.difference(a, b)
        bbox = diff.getbbox()
        if bbox:
            px = sum(1 for p in diff.getdata() if p != (0, 0, 0))
            bad.append((name, f"{px} 像素不同，区域 {(bbox[0]+crop, bbox[1], bbox[2]+crop, bbox[3])}"))
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
