import React from 'react'
import Card from './Card'
import { Section, Field, InfoField } from './form'

// 模型卡（第二级：详细配置）
//
// 这一层只回答"这是什么模型"：属于哪个接口、发给上游的 id 是什么、
// 窗口多大、能不能吃图。**怎么用它**（提示词、工具、默认强度）是配置层的事。
//
// 拆这一层的直接好处：三个 180-low/medium/think 配置指向同一个模型条目，
// 窗口和模态只写一份。
export default function ModelCard({ v, top }) {
  const m = v.editM
  if (!m) return null
  const isNew = !!m._new
  const set = (k, val) => v.setEditM({ ...m, [k]: val })
  const hasImage = (m.input || []).includes('image')
  const toggleImage = () => {
    const cur = m.input || ['text']
    set('input', hasImage ? cur.filter(x => x !== 'image') : [...new Set([...cur, 'image'])])
  }
  const found = v.discovered[m.endpoint] || null
  return (
    <Card kind="col-detail" top={top}
      title={isNew ? '新建模型' : ('模型 · ' + m.id)}
      onClose={() => v.closeModel()}>
      <div className="card-bd">
        <Section>归属</Section>
        <Field label="接口" hint="模型属于某个接口：同一串 id 在不同供应商那儿窗口和模态可能不一样">
          <select value={m.endpoint || ''} disabled={!isNew}
            onChange={e => set('endpoint', e.target.value)}>
            {(v.endpoints || []).map(ep => <option key={ep.name} value={ep.name}>{ep.name}</option>)}
          </select>
        </Field>
        <Field label="模型 id" hint="请求里真正发出去的字符串，要和上游 /models 里的一致">
          <input value={m.id || ''} placeholder="qwen3.8-flash-next-iq3_xxs"
            onChange={e => set('id', e.target.value)} />
        </Field>

        <Section>能力</Section>
        <Field label="窗口大小 (tokens)" hint="留空则由上游 /models 的 meta 推断">
          <input type="number" min="0" value={m.context_window || ''} placeholder="自动"
            onChange={e => set('context_window', e.target.value)} />
        </Field>
        <Field label="输出上限 (tokens)" hint="单次回复（含思考）的上限，留空按 8192">
          <input type="number" min="0" value={m.max_tokens || ''} placeholder="8192"
            onChange={e => set('max_tokens', e.target.value)} />
        </Field>
        <div className={'item static switch-row' + (hasImage ? '' : '')} onClick={toggleImage}>
          <span className="item-title">能接收图像</span>
          <button type="button" role="switch" aria-checked={hasImage}
            className={'tg' + (hasImage ? ' on' : '')}
            onClick={e => { e.stopPropagation(); toggleImage() }}><b /></button>
        </div>
        <div className="set-meta">
          {hasImage ? '会话里可以贴图 / 用截图工具' : '只能处理文字；界面上会拦下图像输入'}
        </div>

        <Section>发现</Section>
        <div className="item static">
          <span className="item-title">{found ? `${found.length} 个` : '还没问过上游'}</span>
          <button type="button" className="btn" disabled={!m.endpoint}
            onClick={() => v.discoverModels(m.endpoint)}>问上游要列表</button>
        </div>
        {found ? (
          found.length === 0 ? <div className="set-meta">这个接口没有返回任何模型</div> : (
            <div className="set-card">
              {found.map(x => (
                <div className="item" key={x.id}>
                  <span className="item-title">{x.id}</span>
                  {x.registered
                    ? <span className="set-meta">已登记</span>
                    : <button type="button" className="btn"
                        onClick={() => v.registerModel(m.endpoint, x.id)}>登记</button>}
                </div>
              ))}
            </div>
          )
        ) : <div className="set-meta">登记过的模型才会出现在配置的模型下拉里</div>}

        {!isNew ? (
          <div className="set-foot-actions">
            <span style={{ flex: 1 }} />
            <button className="btn danger" onClick={v.deleteModel}>删除模型</button>
          </div>
        ) : null}
      </div>
    </Card>
  )
}
