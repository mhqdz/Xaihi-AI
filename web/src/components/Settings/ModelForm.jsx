import React from 'react'
import DirtyActions from './DirtyActions'
import Card from './Card'

// 模型详情 / 新建模型（第二级）
export default function ModelForm({ v, narrow, top }) {
  const editP = v.editP
  if (!editP) return null
  const isNew = !!editP._new
  return (
    <Card kind="col-detail" top={top}
      title={isNew ? '新建模型' : ('模型详情 · ' + editP.name)}
      onClose={() => v.closeOne()}
      actions={<>
        {/* system 提示也算 profile 的字段：改它会让 formDirty 变真，
            但那次保存归编辑器那层（save 的最内层优先）。这里排除掉，
            免得两张卡同时冒出保存按钮，分不清点哪个 */}
        <DirtyActions v={v} dirty={v.formDirty && !v.editorDirty} />
        {!isNew ? <button className="btn danger del-top" onClick={v.deleteProfile}>删除</button> : null}
      </>}>
      <div className="card-bd">
        {[['name', '名称'], ['base_url', '接口地址 (url+/v1)'], ['api_key', 'API Key']].map(([k, label]) => (
          <label className="field" key={k}><span>{label}</span>
            <input value={editP[k] || ''} type={k === 'api_key' ? 'password' : 'text'}
              onChange={e => v.setEditP({ ...editP, [k]: e.target.value })} /></label>
        ))}
        <label className="field"><span className="field-label">模型</span>
          {v.models.loading ? <span className="item-desc">正在匹配模型…</span>
            : v.models.err ? <span className="item-desc">检查接口地址或 API Key：{v.models.err}</span>
            : v.models.list.length ? (
              <select value={editP.model || ''} onChange={e => v.setEditP({ ...editP, model: e.target.value })}>
                {v.models.list.map(m => <option key={m} value={m}>{m}</option>)}
              </select>
            ) : <input value={editP.model || ''} onChange={e => v.setEditP({ ...editP, model: e.target.value })} placeholder="没取到模型列表，可手填" />}
        </label>
        <label className="field"><span>思考强度</span>
          <select value={editP.reasoning_effort || 'none'} onChange={e => v.setEditP({ ...editP, reasoning_effort: e.target.value })}>
            {['none', 'low', 'medium', 'high', 'max'].map(x => <option key={x} value={x}>{x}</option>)}
          </select></label>
        <label className="field"><span>服务商（云端 OpenAI 兼容填 openai：不发本地扩展字段）</span>
          <select value={editP.provider || ''} onChange={e => v.setEditP({ ...editP, provider: e.target.value })}>
            <option value="">自动 / 本地（Strata、llama.cpp）</option>
            <option value="openai">openai（云端兼容接口）</option>
          </select></label>
        <label className="field"><span>上下文窗口（tokens，留空=自动读上游）</span>
          <input type="number" min="0" max="2000000" placeholder="自动（如 131072）"
            value={editP.context_window || ''}
            onChange={e => v.setEditP({ ...editP, context_window: Number(e.target.value) || 0 })} />
        </label>
        <label className="field"><span>输入上限（tokens，留空=105000）</span>
          <input type="number" min="0" max="1000000" placeholder="105000"
            value={editP.max_prompt_tokens || ''}
            onChange={e => v.setEditP({ ...editP, max_prompt_tokens: Number(e.target.value) || 0 })} />
        </label>
        <label className="field"><span>压缩阈值（%，留空=80）</span>
          <input type="number" min="1" max="99" placeholder="80"
            value={editP.compact_pct || ''}
            onChange={e => v.setEditP({ ...editP, compact_pct: Number(e.target.value) || 0 })} />
        </label>
        <div className="field"><span>工具</span>
          <div className="choices">
            {v.toolList.map(t => {
              const on = (editP.tools || '').split(',').map(x => x.trim()).filter(Boolean).includes(t.name)
              return (
                <label key={t.name} title={t.description || ''} className={'choice' + (on ? ' on' : '')}>
                  <input type="checkbox" checked={on} onChange={e => {
                    const cur = (editP.tools || '').split(',').map(x => x.trim()).filter(Boolean)
                    const next = e.target.checked ? [...cur, t.name] : cur.filter(x => x !== t.name)
                    v.setEditP({ ...editP, tools: next.join(',') })
                  }} />
                  <span>{t.name}</span>
                </label>
              )
            })}
          </div>
        </div>
        <div className="item" onClick={v.openSystemEditor}>
          <span className="item-title">system</span>
          <span className="item-desc">{editP.system || '（空）'}</span>
        </div>
        {!isNew ? (
          <div className="card-foot del-bottom-wrap">
            <button className="btn danger del-bottom" onClick={v.deleteProfile}>删除配置</button>
          </div>
        ) : null}
      </div>
    </Card>
  )
}
