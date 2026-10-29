import React, { useEffect, useRef } from 'react'
import DirtyActions from './DirtyActions'
import XIcon from './XIcon'

// 文本域随内容长高（写 CSS 变量，避免被 !important 压掉）
function autoGrow(el) {
  if (!el) return
  el.style.height = 'auto'
  let h = el.scrollHeight + 2
  if (el.classList.contains('set-area')) {
    // 上限来自卡片自身的 max-height（由 CSS 决定，宽窄屏一致），减去标题与字数栏
    const card = el.closest('.card')
    let cap = Infinity
    if (card && card.parentElement) {
      const box = card.parentElement
      const cs = getComputedStyle(box)
      const inner = box.clientHeight - parseFloat(cs.paddingTop || 0) - parseFloat(cs.paddingBottom || 0)
      const hd = card.querySelector('.card-hd')
      const ft = card.querySelector('.set-col-foot')
      cap = inner - (hd ? hd.offsetHeight : 0) - (ft ? ft.offsetHeight : 0) - 4
    }
    el.style.overflowY = h > cap ? 'auto' : 'hidden'
    h = Math.min(h, Math.max(160, cap))
  }
  el.style.setProperty('--h', h + 'px')
  el.style.height = h + 'px'
}

// 三级编辑卡：system 提示 / 工具说明
export default function EditorCard({ v, narrow }) {
  const isTool = !!v.toolEdit
  const value = isTool ? (v.toolEdit.description || '') : (v.editP?.[v.editField] || '')
  const ta = useRef(null)
  useEffect(() => {
    const el = ta.current
    if (el) setTimeout(() => autoGrow(el), 0)
  }, [v.editField, v.toolEdit])
  return (
    <div className="card col-editor is-top">
      <div className="card-hd">
        <button className="icon-x back card-back narrow-only" onClick={() => v.closeOne()} aria-label="关闭" title="关闭"><XIcon /></button>
        <span className="ef-label"><span className="ef-label-t">{isTool ? ('tool · ' + v.toolEdit.name) : 'system'}</span></span>
        <span style={{ flex: 1 }} />
        <DirtyActions v={v} show={narrow} />
      </div>
      <textarea ref={el => { ta.current = el; if (el) autoGrow(el) }} className="set-area" value={value}
        onInput={e => autoGrow(e.target)}
        placeholder={isTool ? '工具说明（模型会看到这段文字）…' : '这里写完整的 system 提示…'}
        onChange={e => {
          if (isTool) v.setToolEdit({ ...v.toolEdit, description: e.target.value })
          else v.setEditP({ ...v.editP, [v.editField]: e.target.value })
        }} />
      <div className="set-col-foot"><span className="set-meta">{value.length} 字</span></div>
    </div>
  )
}
