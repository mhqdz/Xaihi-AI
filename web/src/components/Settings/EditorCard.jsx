import React from 'react'
import Card from './Card'

// 三级编辑卡：system 提示 / 工具说明（与二级卡同容器、同标题栏；高度填满可用区域）
export default function EditorCard({ v, narrow, top }) {
  const isTool = !!v.toolEdit
  const value = isTool ? (v.toolEdit.description || '') : (v.editP?.[v.editField] || '')
  return (
    <Card kind="col-editor" top={top}
      title={isTool ? ('tool · ' + v.toolEdit.name) : 'system'}
      onClose={() => v.closeOne()}
>
      <textarea className="set-area" aria-label={isTool ? '工具说明' : 'system 提示词'} value={value}
        placeholder={isTool ? '工具说明（模型会看到这段文字）…' : '这里写完整的 system 提示…'}
        onChange={e => {
          if (isTool) v.setToolEdit({ ...v.toolEdit, description: e.target.value })
          else v.setEditP({ ...v.editP, [v.editField]: e.target.value })
        }} />
      <div className="set-col-foot"><span className="set-meta">{value.length} 字</span></div>
    </Card>
  )
}
