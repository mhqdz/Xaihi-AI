import React from 'react'

// 保存 / 取消：只作用于"最内层脏的东西"（编辑器 → profile → 常规配置）
// 宽屏时挂在列表卡头上，窄屏时挂在当前那级卡头上
export default function DirtyActions({ v, show }) {
  if (!v.anyDirty || !show) return null
  return (
    <span className="card-actions title-actions">
      <button className="btn" onClick={v.cancel}>取消</button>
      <button className="btn" onClick={v.save}>保存</button>
    </span>
  )
}
