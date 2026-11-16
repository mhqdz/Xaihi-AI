#!/usr/bin/env python3
"""看状态 / 发动作。用法:
  python3 probe.py                     # 打印当前状态摘要
  python3 probe.py '{"action":"click","index":0}'   # 发动作，等 1.5s 再打印
"""
import json, os, sys, time

D = os.environ.get("STS2_BRIDGE_DATA") or os.path.join(
    os.environ.get("XDG_DATA_HOME", os.path.expanduser("~/.local/share")),
    "slaythespire2", "bridge")
SP, AP = os.path.join(D, "state.json"), os.path.join(D, "action.json")


def load():
    with open(SP) as f:
        return json.load(f)


def show(d):
    print(f"scene={d['scene']} seq={d.get('seq')} floor={d.get('floor')}")
    if d.get("last_error"):
        print("  !! last_error:", d["last_error"])
    if d.get("probe"):
        print("  probe:", json.dumps(d["probe"], ensure_ascii=False))
    p = d.get("player")
    if p:
        print(f"  player hp={p['hp']}/{p['max_hp']} gold={p.get('gold')} "
              f"energy={p.get('energy')}/{p.get('max_energy')} block={p.get('block')}")
    if d.get("hand"):
        print("  hand:", ", ".join(f"[{c['index']}]{c['name']}({c.get('cost')})"
                                   f"{'*' if c['playable'] else ''}" for c in d["hand"]))
    if d.get("enemies"):
        for e in d["enemies"]:
            print(f"  enemy[{e['index']}] {e['name']} {e['hp']}/{e['max_hp']} "
                  f"block={e.get('block')} intent={e.get('intent')}")
    for key in ("items", "characters", "rewards", "nodes", "options", "cards"):
        if d.get(key):
            print(f"  {key}:")
            for it in d[key]:
                extra = {k: v for k, v in it.items() if k not in ("index",)}
                print(f"    [{it['index']}]", json.dumps(extra, ensure_ascii=False)[:150])
    if d.get("event"):
        print("  event:", json.dumps(d["event"], ensure_ascii=False)[:200])
    if d.get("prompt"):
        print("  prompt:", d["prompt"][:120])
    acts = d.get("available_actions") or []
    seen, out = set(), []
    for a in acts:
        key = a["id"]
        if key in ("click", "menu_click") or key in seen:
            continue
        seen.add(key)
        out.append(json.dumps(a, ensure_ascii=False))
    print(f"  actions({len(acts)}):", " ".join(out[:14]) or "(只有 click)")
    if d.get("clickables"):
        print(f"  clickables({len(d['clickables'])}):",
              " | ".join(f"[{c['index']}]{c['label'][:24]}".replace("\n", " ")
                         for c in d["clickables"][:14]))


if len(sys.argv) > 1:
    before = load().get("seq")
    tmp = AP + ".tmp"
    with open(tmp, "w") as f:
        f.write(sys.argv[1])
    os.replace(tmp, AP)  # 原子替换：mod 那边改名取走，不会读到半截文件
    for _ in range(30):
        time.sleep(0.3)
        d = load()
        if d.get("seq") != before:
            show(d)
            break
    else:
        print("(状态没变)")
else:
    show(load())
