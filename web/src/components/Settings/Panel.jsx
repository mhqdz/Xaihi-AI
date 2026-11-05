import React from 'react'
import EditorCard from './EditorCard'
import ToolCard from './ToolCard'
import ModelForm from './ModelForm'
import ListCard from './ListCard'
import DirtyActions from './DirtyActions'
import SaveSheet from './SaveSheet'
import EndpointForm from './EndpointForm'
import ModelCard from './ModelCard'

// 设置面板（装配层）：编辑器 / 工具 / 模型 / 列表 四张卡按状态互斥出现
export default function SettingsPanel({ v, narrow, profiles, uiCfg, openConv }) {
  // 层级：3 级编辑器 / 2 级详情（模型或工具，二者互斥）/ 1 级列表
  // 列宽按"实际渲染几张卡"算，同一时刻最多三张
  const editing = !!(v.editField || v.toolEdit)
  const detail = !!(v.toolForm || v.editP || v.editE || v.editM)
  const cols = editing && detail ? ' three' : (editing || detail) ? ' two' : ''
  // 只有一张"最上层"卡（窄屏只显示它）
  const top = editing ? 'editor' : detail ? 'detail' : 'list'
  return (
    <aside className={'settings' + cols}>
      {/* 背景在打开时是 inert 的（见 App.jsx），这里补上 dialog 语义，
          读屏才知道这是个能关掉的对话框，而不是页面里又一段内容 */}
      <div className={'set-cols' + cols} role="dialog" aria-modal="true" aria-label="设置"
        onClick={e => { if (e.target === e.currentTarget) v.closeOne() }}>
        {editing ? <EditorCard v={v} narrow={narrow} top={top === 'editor'} /> : null}
        {v.toolForm ? <ToolCard v={v} narrow={narrow} profiles={profiles} top={top === 'detail'} /> : null}
        {v.editE ? <EndpointForm v={v} top={top === 'detail'} /> : null}
        {v.editM ? <ModelCard v={v} top={top === 'detail'} /> : null}
        {v.editP ? <ModelForm v={v} narrow={narrow} top={top === 'detail'} /> : null}
        <ListCard v={v} narrow={narrow} profiles={profiles} uiCfg={uiCfg} openConv={openConv} top={top === 'list'} />
      </div>
      {/* 窄屏时一级卡是隐藏的，保存/取消放这儿，任何层级都够得着。
          宽屏由一级卡自己带，这里不渲染（CSS 也兜了一层）。 */}
      {narrow && v.anyDirty ? (
        <div className="set-actions">
          <DirtyActions v={v} dirty={v.anyDirty} />
        </div>
      ) : null}
      <SaveSheet v={v} />
    </aside>
  )
}
