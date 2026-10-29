import React from 'react'
import EditorCard from './EditorCard'
import ToolCard from './ToolCard'
import ModelForm from './ModelForm'
import ListCard from './ListCard'

// 设置面板（装配层）：编辑器 / 工具 / 模型 / 列表 四张卡按状态互斥出现
export default function SettingsPanel({ v, narrow, profiles, uiCfg, openConv }) {
  const editing = v.editField || v.toolEdit
  const cols = editing ? (v.editP ? ' three' : ' two') : ((v.editP || v.toolCfg) ? ' two' : '')
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
