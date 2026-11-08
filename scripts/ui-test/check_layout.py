import sys, os, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wd import Session, req

BASE = "http://127.0.0.1:18400"
FAIL = []
def check(name, cond, extra=""):
    print(("  ✓ " if cond else "  ✗ ") + name + (f"  {extra}" if extra else ""))
    if not cond: FAIL.append(name)

CARDS = """return [...document.querySelectorAll('.set-cols > .card')].map(e => {
  const r = e.getBoundingClientRect(); const cs = getComputedStyle(e);
  return { cls: e.className.replace(' is-top',''), top: e.className.includes('is-top'), disp: cs.display,
           x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height),
           title: e.querySelector('.ef-label-t')?.textContent };
}).filter(c => c.disp !== 'none')"""

def open_settings(s):
    s.get(BASE + "/?settings=1&conv=51"); time.sleep(2.2)

# ─────────── 宽屏：2 级（模型/工具）与 3 级必须是同一套几何 ───────────
s = Session(1600, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 1600, "height": 1000})
open_settings(s)
base = s.js(CARDS)
check("宽屏初始只有列表卡", len(base) == 1 and base[0]['cls'] == 'card col-list', str(base))

# 按名字找模型：「接口」分组的条目也是 .item.col，取第一个会点到接口卡
s.js("[...document.querySelectorAll('.col-list .item.col')].find(e=>/180-low/.test(e.textContent)).click(); return 1"); time.sleep(1.0)
model2 = s.js(CARDS)
# 3 级的入口是 LinkRow（配置卡的 system / 工具卡的 说明）：
# 用 .item 会点到"能接收图像"那种开关行
s.js("document.querySelector('.col-detail .item.link-row').click(); return 1"); time.sleep(1.0)
model3 = s.js(CARDS)

open_settings(s)
s.js("[...document.querySelectorAll('.col-list .item')].find(e=>e.textContent.includes('bash')).click(); return 1"); time.sleep(1.0)
tool2 = s.js(CARDS)
# 3 级的入口是 LinkRow（配置卡的 system / 工具卡的 说明）：
# 用 .item 会点到"能接收图像"那种开关行
s.js("document.querySelector('.col-detail .item.link-row').click(); return 1"); time.sleep(1.0)
tool3 = s.js(CARDS)

def geom(c): return [c['x'], c['y'], c['w'], c['h']]
check("2 级：模型与工具同一容器类", model2[0]['cls'] == tool2[0]['cls'] == 'card col-detail', f"{model2[0]['cls']} / {tool2[0]['cls']}")
check("2 级：模型与工具几何完全一致", geom(model2[0]) == geom(tool2[0]), f"{geom(model2[0])} vs {geom(tool2[0])}")
check("3 级：模型与工具同一容器类", model3[0]['cls'] == tool3[0]['cls'] == 'card col-editor', f"{model3[0]['cls']} / {tool3[0]['cls']}")
check("3 级：模型与工具几何完全一致", geom(model3[0]) == geom(tool3[0]), f"{geom(model3[0])} vs {geom(tool3[0])}")
check("宽屏三张卡都撑满高度", len(model3) == 3 and len({c['h'] for c in model3}) == 1, str([c['h'] for c in model3]))
check("宽屏三张卡同一顶线", len({c['y'] for c in model3}) == 1, str([c['y'] for c in model3]))
check("3 级卡比 2 级卡宽（470 vs 400）", model3[0]['w'] == 470 and model3[1]['w'] == 400, f"{model3[0]['w']} vs {model3[1]['w']}")
check("2 级只有一张 is-top，列表不是", model2[0]['top'] and model2[1]['cls'] == 'card col-list' and not model2[1]['top'], str([(c['cls'], c['top']) for c in model2]))
check("3 级时只有编辑器 is-top", model3[0]['top'] and not model3[1]['top'] and not model3[2]['top'], str([(c['cls'], c['top']) for c in model3]))
s.close()

# ─────────── 窄屏：任何层级都只显示一张卡，且尺寸一致 ───────────
s = Session(900, 1000)
req("POST", f"/session/{s.id}/window/rect", {"width": 900, "height": 1000})
open_settings(s)
states = {'初始': s.js(CARDS)}
# 按名字找模型：「接口」分组的条目也是 .item.col，取第一个会点到接口卡
s.js("[...document.querySelectorAll('.col-list .item.col')].find(e=>/180-low/.test(e.textContent)).click(); return 1"); time.sleep(1.0)
states['模型2'] = s.js(CARDS)
# 3 级的入口是 LinkRow（配置卡的 system / 工具卡的 说明）：
# 用 .item 会点到"能接收图像"那种开关行
s.js("document.querySelector('.col-detail .item.link-row').click(); return 1"); time.sleep(1.0)
states['模型3'] = s.js(CARDS)
open_settings(s)
s.js("[...document.querySelectorAll('.col-list .item')].find(e=>e.textContent.includes('bash')).click(); return 1"); time.sleep(1.0)
states['工具2'] = s.js(CARDS)
# 3 级的入口是 LinkRow（配置卡的 system / 工具卡的 说明）：
# 用 .item 会点到"能接收图像"那种开关行
s.js("document.querySelector('.col-detail .item.link-row').click(); return 1"); time.sleep(1.0)
states['工具3'] = s.js(CARDS)

for k, v in states.items():
    check(f"窄屏·{k}：只显示一张卡", len(v) == 1, str([c['cls'] for c in v]))
for k, v in states.items():
    if not v: continue
    check(f"窄屏·{k}：尺寸一致 886×862 @44", [v[0]['w'], v[0]['h'], v[0]['y']] == [886, 862, 44], str([v[0]['w'], v[0]['h'], v[0]['y']]))
check("窄屏 2 级：模型与工具同类", states['模型2'][0]['cls'] == states['工具2'][0]['cls'] == 'card col-detail')
check("窄屏 3 级：模型与工具同类", states['模型3'][0]['cls'] == states['工具3'][0]['cls'] == 'card col-editor')
check("窄屏 3 级：编辑区填满卡片",
      (s.js("""const a=document.querySelector('.col-editor .set-area'), c=document.querySelector('.col-editor');
               return Math.round(a.getBoundingClientRect().height) > Math.round(c.getBoundingClientRect().height) * 0.8""") == True))
s.shot('/tmp/uitest/layout_narrow.png')

# ─────────── 阅读栏宽度上限：铺满可用宽度，而不是卡在写死的数字 ───────────
# 曾经 Math.min(1400, …) —— 屏幕更宽时拖到 1400 就再也不动，看起来像"文本被设了最大宽度"
# 用一个"足够宽"的窗口量：1400 的旧上限只有在舞台 >1432 时才咬得住
req("POST", f"/session/{s.id}/window/rect", {"width": 2000, "height": 1000})
s.js("localStorage.setItem('colW','3000'); return 1")
s.get(BASE + "/?conv=51"); time.sleep(2.2)
mm = s.js("""const c=document.querySelector('.col'), st=document.querySelector('.stage'),
      cp=document.querySelector('.composer');
  return {col: Math.round(c.getBoundingClientRect().width), stage: st.clientWidth,
          cr: Math.round(c.getBoundingClientRect().right), pr: Math.round(cp.getBoundingClientRect().right)}""")
# 拉满 = "有边距的满"：每侧留 min(48px,3vw)+34，所以列占舞台 84%~94%
mm2 = s.js("""const c=document.querySelector('.col'), st=document.querySelector('.stage');
  const S=st.clientWidth, col=Math.round(c.getBoundingClientRect().width);
  return {S, col, gap: Math.round((S-col)/2), ratio: +(col/S).toFixed(3)}""")
# 拉满 = 两侧明显留白（每侧 34 + 舞台×18%），列占舞台 50%~70%
check("拉满时两侧明显留白（列占舞台 50%~70%）", mm2 and 0.50 <= mm2["ratio"] <= 0.72, str(mm2))
check("每侧留白 ≥ 舞台 15%", mm2 and mm2["gap"] >= mm2["S"] * 0.15, str(mm2))
check("阅读栏没有超出舞台", mm2 and mm2["col"] <= mm2["S"], str(mm2))
# 留白跟着列宽线性张开：丰富（560）时只有拖动线要的 34，宽栏时加上舞台的 18%
gut = s.js("""const st=document.querySelector('.stage');
  const g=()=>parseFloat(getComputedStyle(st).getPropertyValue('--page-gutter'));
  return {now: g()}""")
check("留白已经按比例张开（>100px）", gut and gut["now"] > 100, str(gut))
check("正文与输入框右缘对齐", mm and mm["cr"] == mm["pr"], str(mm))

# 拖动线（列左缘 -22px）不能和左边栏的边叠在一起：抽屉右缘有 6px 热区
gap = s.js("""const col=document.querySelector('.col'), dr=document.querySelector('.drawer');
  const cl=Math.round(col.getBoundingClientRect().left);
  const drw=Math.round(dr.getBoundingClientRect().right);
  return {lineX: cl-22, drawerRight: drw, gap: (cl-22)-drw}""")
check("拖动线不压左边栏（间隔 ≥ 8px）", gap and gap["gap"] >= 8, str(gap))

# 关键回归：窗口缩小再放大，列宽必须回到原来的宽度。
# 曾经在 resize 里 setColW(min(colW, 可用))，于是缩一次就永久变窄（棘轮）。
req("POST", f"/session/{s.id}/window/rect", {"width": 1200, "height": 1000})
s.get(BASE + "/?conv=51"); time.sleep(1.6)
small = s.js("return Math.round(document.querySelector('.col').getBoundingClientRect().width)")
req("POST", f"/session/{s.id}/window/rect", {"width": 2000, "height": 1000})
s.get(BASE + "/?conv=51"); time.sleep(1.6)
back = s.js("""const c=document.querySelector('.col'), st=document.querySelector('.stage');
  return {col: Math.round(c.getBoundingClientRect().width), stage: st.clientWidth}""")
check("缩小再放大后列宽能长回来", back and back["col"] == mm["col"],
      f"{mm['col']} → {small} → {back['col']}")

# 正文必须跟着列宽走：endfield 的 .ef-prose 自带 max-width:40rem（640px），
# 不覆盖的话栏拖到 1400，正文仍停在 640，右边全是白
pw = s.js("""const col=document.querySelector('.col'), pr=document.querySelector('.msg .ef-prose');
  if(!pr) return null;
  return {col: Math.round(col.getBoundingClientRect().width), prose: Math.round(pr.getBoundingClientRect().width)}""")
check("正文宽度跟随阅读栏（不再被 640 卡住）", pw and pw["prose"] > pw["col"] * 0.9, str(pw))
s.js("localStorage.setItem('colW','960'); return 1")
s.close()
s.js("localStorage.setItem('colW','960'); return 1")
s.close()
print("失败:", "、".join(FAIL) if FAIL else "无")
