import sys, os, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req
B="http://127.0.0.1:18400"
s=Session(1500,1000); req("POST", f"/session/{s.id}/window/rect",{"width":1500,"height":1000})
s.get(B+"/?conv=51"); time.sleep(3)
print("初始:", s.js("const t=document.querySelector('.thread'); return [t.scrollHeight,t.scrollTop].join('/')"))
for i in range(3):
    s.js("document.querySelector('.thread').scrollTop=0; return 1")
    time.sleep(2.5)
    print(f"第{i+1}次滚到顶后:", s.js("const t=document.querySelector('.thread'); return [t.scrollHeight,Math.round(t.scrollTop)].join('/')"),
          "消息数:", s.js("return document.querySelectorAll('.thread [data-seq]').length"))
s.close()
