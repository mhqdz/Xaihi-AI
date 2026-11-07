import React from 'react'
import Card from './Card'
import { Section, Field } from './form'

// 接口卡（第二级：详细配置）
//
// 这里只关心"接哪儿"：来源 / 名称 / 地址 / 凭据。
// 用哪个模型、什么思考强度、system 提示，都是模型卡的事。
//
// 「来源模板」是**套用**而不是"填空"：切一个模板，地址和协议就换成那个模板的。
// （曾经只填"空着的字段"，于是选了第一个模板之后再切别的什么都不会变 ——
//  看起来就是选择器坏了。）
//
// 「可用模型」是**当场检验**：新建时地址(+key)填完自动问一次上游，
// 通了显示模型清单、不通直接说原因（缺 key / 401 / 连不上）。
// 手动按钮叫「更新信息」，改完地址或 key 再点一次即可。
const PROBE_TEXT = {
  idle: '还没检验',
  waitkey: '填上 API Key 就会检验',
  running: '正在问上游…'
}

export default function EndpointForm({ v, top }) {
  const e = v.editE
  if (!e) return null
  const isNew = !!e._new
  const set = (k, val) => v.setEditE({ ...e, [k]: val })
  const probe = v.probe || { state: 'idle' }
  const models = probe.models || []
  const pickSource = (key) => {
    const src = (v.sources || []).find(s => s.key === key)
    v.setEditE({
      ...e, source: key,
      // 套用模板 = 地址和协议跟着换；选"不套模板"就是把它们清空
      base_url: src ? (src.base_url || '') : '',
      provider: src ? (src.provider || '') : ''
    })
  }
  const status = probe.state === 'ok'
    ? `${models.length} 个模型`
    : (probe.state === 'error' ? '没通' : (PROBE_TEXT[probe.state] || PROBE_TEXT.idle))
  return (
    <Card kind="col-detail" top={top}
      title={isNew ? '新建接口' : ('接口 · ' + e.name)}
      dirty={v.editEDirty}
      onClose={() => v.closeEndpoint()}>
      <div className="card-bd">
        <Section>来源</Section>
        <Field label="来源模板" hint="切一个模板会把下面的地址和协议换成它的；清单在 sources.json，可以自己加">
          <select value={e.source || ''} onChange={ev => pickSource(ev.target.value)}>
            <option value="">（不套模板）</option>
            {(v.sources || []).map(s => <option key={s.key} value={s.key}>{s.label}</option>)}
          </select>
        </Field>

        <Section>连接</Section>
        <Field label="名称"
          hint={isNew ? '给这个接口起个短名字，模型通过它来引用' : '名字是模型引用它的键，不能改 —— 改名等于换一个接口'}>
          {/* 只读用 readOnly 而不是 disabled：disabled 的文字被 UA 调淡，
              而且不能选中复制 —— 接口名/模型 id 恰恰是会被复制出去的东西 */}
          <input className={isNew ? '' : 'ro'} value={e.name || ''} readOnly={!isNew}
            onChange={ev => set('name', ev.target.value)} />
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
          <span className="item-title">{status}</span>
          <button type="button" className="btn" disabled={!e.base_url || probe.state === 'running'}
            onClick={() => v.probeEndpoint()}>更新信息</button>
        </div>
        {probe.state === 'error' ? (
          <div className="probe-err">{probe.error}</div>
        ) : null}
        {probe.state === 'ok' ? (
          <div className="ro-list">{models.length ? models.map(m => m.id).join('\n') : '这个接口没有返回任何模型'}</div>
        ) : null}
        <div className="set-meta">
          通了就可以去「模型」那层登记：点开一个模型条目、问上游要列表、挑一个登记
        </div>

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
