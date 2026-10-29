import React from 'react'
import EditorCard from './EditorCard'
import ToolCard from './ToolCard'
import ModelForm from './ModelForm'
import ListCard from './ListCard'

// 设置面板（装配层）：编辑器 / 工具 / 模型 / 列表 四张卡按状态互斥出现
export default function SettingsPanel({ v, narrow, profiles, uiCfg, openConv }) {
  const editing = v.editField || v.toolEdit
  // 列宽按"实际渲染几张卡"算：编辑器 / 工具 / 模型 / 列表（列表恒在）
  // 旧逻辑只看 editP，工具说明编辑时三张卡却给了 two（编辑器不加宽）
  const cards = 1 + (editing ? 1 : 0) + ((v.toolCfg || v.toolForm) ? 1 : 0) + (v.editP ? 1 : 0)
  const cols = cards >= 3 ? ' three' : cards === 2 ? ' two' : ''
  return (
    <aside className={'settings' + cols}>
      <div className={'set-cols' + cols}
        onClick={e => { if (e.target === e.currentTarget) v.closeOne() }}>
        {editing ? <EditorCard v={v} narrow={narrow} /> : null}
        {(v.toolCfg || v.toolForm) ? <ToolCard v={v} narrow={narrow} profiles={profiles} /> : null}
        {v.editP ? <ModelForm v={v} narrow={narrow} /> : null}
        <ListCard v={v} narrow={narrow} profiles={profiles} uiCfg={uiCfg} openConv={openConv} />
      </div>
    </aside>
  )
}
