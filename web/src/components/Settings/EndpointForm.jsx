import React from 'react'
import Card from './Card'
import { Section, Field } from './form'

// 接口卡（第二级：详细配置）
//
// 这里只关心"接哪儿"：来源 / 名称 / 地址 / 凭据。
// 用哪个模型、什么思考强度、system 提示，都是模型卡的事。
// 选来源会把地址模板和协议预填进来（来源清单来自 sources.json，加一条就多一个，
// 不用改代码）。
export default function EndpointForm({ v, top }) {
  const e = v.editE
  if (!e) return null
  const isNew = !!e._new
  const set = (k, val) => v.setEditE({ ...e, [k]: val })
  const models = v.endpointModels[e.name] || null
  const pickSource = (key) => {
    const src = (v.sources || []).find(s => s.key === key)
    if (!src) { set('source', ''); return }
    v.setEditE({
      ...e, source: src.key,
      // 只预填空着的字段，不覆盖用户已经写好的地址
      base_url: e.base_url && e.base_url.trim() ? e.base_url : (src.base_url || ''),
      provider: e.provider || src.provider || ''
    })
  }
  return (
    <Card kind="col-detail" top={top}
      title={isNew ? '新建接口' : ('接口 · ' + e.name)}
      onClose={() => v.closeEndpoint()}>
      <div className="card-bd">
        <Section>来源</Section>
        <Field label="来源模板" hint="选了会把地址和协议预填进来；清单在 sources.json，可以自己加">
          <select value={e.source || ''} onChange={ev => pickSource(ev.target.value)}>
            <option value="">（不套模板）</option>
            {(v.sources || []).map(s => <option key={s.key} value={s.key}>{s.label}</option>)}
          </select>
        </Field>

        <Section>连接</Section>
        <Field label="名称" hint="给这个接口起个短名字，模型通过它来引用">
          <input value={e.name || ''} onChange={ev => set('name', ev.target.value)} />
        </Field>
        <Field label="接口地址" hint="填到 /v1 为止，不带 /chat/completions">
          <input value={e.base_url || ''} placeholder="http://127.0.0.1:18320/180/v1"
            onChange={ev => set('base_url', ev.target.value)} />
        </Field>
        <Field label="API Key" hint="留空则回落全局配置里的 key">
          <input type="password" value={e.api_key || ''} onChange={ev => set('api_key', ev.target.value)} />
        </Field>
        <Field label="协议" hint="云端 OpenAI 兼容选 openai（不发本地扩展字段），自建保持自动">
          <select value={e.provider || ''} onChange={ev => set('provider', ev.target.value)}>
            <option value="">自动（本地 / Strata / llama.cpp）</option>
            <option value="openai">openai（云端兼容接口）</option>
          </select>
        </Field>

        <Section>可用模型</Section>
        <div className="item static">
          <span className="item-title">{models ? `${models.length} 个模型` : '还没拉取'}</span>
          <button type="button" className="btn" disabled={isNew || !e.name}
            onClick={() => v.fetchEndpointModels(e.name)}>拉取列表</button>
        </div>
        {models ? (
          <div className="tool-meta tool-cmd">{models.length ? models.join('\n') : '这个接口没有返回任何模型'}</div>
        ) : <div className="set-meta">接口保存后可以拉一次，确认地址和 key 是通的</div>}

        {!isNew ? (
          <div className="set-foot-actions">
            <span style={{ flex: 1 }} />
            <button className="btn danger" onClick={v.deleteEndpoint}>删除接口</button>
          </div>
        ) : null}
      </div>
    </Card>
  )
}
