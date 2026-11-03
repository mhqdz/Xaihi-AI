import React, { useEffect, useRef } from 'react'

// 保存 / 取消：只作用于"最内层脏的东西"（编辑器 → profile → 常规配置）。
//
// 每个卡片只在自己那一层脏时出现 —— 不再"宽屏时全飘到一级卡、窄屏时才落回本卡"。
// 那样按钮离你改的内容很远，而且窄屏还要靠窗口宽度决定它出现在哪。
//
// dirty 由调用方给（settingsDirty / formDirty / editorDirty），不是全局 anyDirty：
// 否则改模型详情时，旁边两张卡也会冒出保存按钮。
//
// v.nudge 每次 +1 表示"用户想关面板但这里还没存"：按钮跳两下 + 描边高亮，
// 把人指回这两个按钮本身，而不是弹一个要读字、要再点确认的对话框。
export default function DirtyActions({ v, dirty }) {
  const ref = useRef(null)
  useEffect(() => {
    if (!v.nudge || !dirty) return
    const el = ref.current
    if (!el) return
    el.classList.remove('nudge')
    void el.offsetWidth          // 强制 reflow：连按两次 Esc 也要能重放动画
    el.classList.add('nudge')
  }, [v.nudge, dirty])
  if (!dirty) return null
  return (
    <span className="card-actions title-actions" ref={ref}>
      <button className="btn" onClick={v.cancel}>取消</button>
      <button className="btn save" onClick={v.save}>保存</button>
    </span>
  )
}
