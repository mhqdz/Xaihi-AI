#!/usr/bin/env python3
"""临时：截「模型详情」表单（含 3 个 select），用于改前改后对比。
用法： python3 scripts/ui-test/_shot_model.py <输出.png>
"""
import sys, os, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

out = sys.argv[1] if len(sys.argv) > 1 else "/tmp/uitest/model.png"
B = "http://127.0.0.1:18400"

s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
s.get(B + "/?settings=1&conv=51")
time.sleep(1.8)
s.js("document.querySelector('.col-list .item.col').click(); return true")
time.sleep(1.2)
info = s.js("""const sels=[...document.querySelectorAll('.col-detail select')];
  return sels.map(x=>{const c=getComputedStyle(x);
    return {n:x.options.length, app:c.appearance, bg:c.backgroundColor, img:c.backgroundImage.slice(0,40),
            h:Math.round(x.getBoundingClientRect().height), w:Math.round(x.getBoundingClientRect().width)}})""")
print("selects:", info)
num = s.js("""const n=[...document.querySelectorAll('.col-detail input[type=number]')];
  return n.map(x=>{const r=x.getBoundingClientRect(); return [Math.round(r.width), x.value]})""")
print("number inputs:", num)
ih = s.js("""const n=[...document.querySelectorAll('.col-detail .field input')];
  return n.map(x=>[x.type, Math.round(x.getBoundingClientRect().height)])""")
print("field inputs:", ih)
s.shot(out)
print("saved", out)
s.close()
