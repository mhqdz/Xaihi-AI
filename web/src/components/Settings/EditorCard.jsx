import React from 'react'
import Card from './Card'

// 三级编辑卡：system 提示 / 工具说明（与二级卡同容器、同标题栏；高度填满可用区域）
export default function EditorCard({ v, narrow, top }) {
  const isPersona = !!v.personaEdit
  if (isPersona) {
    const pe = v.personaEdit
    return (
      <Card kind="col-editor" top={top}
        title={'人格 · ' + (pe.name || '（未命名）')}
        onClose={() => v.closePersonaEditor()}
        dirty={v.personaDirty}>
        {pe.isNew ? (
          <div className="field">
            <span>名字</span>
            <input value={pe.name} placeholder="a-z0-9_-"
              onChange={e => v.setPersonaEdit({ ...pe, name: e.target.value })} />
            <span className="field-hint">存成 personas/&lt;名字&gt;.md；改名就是新建</span>
          </div>
        ) : null}
        <textarea className="set-area" aria-label="人格"
          value={pe.body} placeholder="她是谁、怎么说话、不做什么…"
          onChange={e => v.setPersonaEdit({ ...pe, body: e.target.value })} />
        <div className="set-col-foot">
          <span className="set-meta">{pe.body.length} 字</span>
          <button className="btn tiny" disabled={!pe.name.trim() || v.personaBusy}
            onClick={() => v.savePersona()}>保存人格</button>
        </div>
      </Card>
    )
  }
  const isTool = !!v.toolEdit
  const value = isTool ? (v.toolEdit.description || '') : (v.editP?.[v.editField] || '')
  return (
    <Card kind="col-editor" top={top}
      title={isTool ? ('tool · ' + v.toolEdit.name) : 'system'}
      onClose={() => v.closeOne()}
      dirty={v.editorDirty}>
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
