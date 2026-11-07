import React from 'react'
import Card from './Card'
import { Section, Field, LinkRow, ChoiceGrid, SwitchRow, TagInput } from './form'

// 配置卡（第二级：详细配置）
//
// 两层结构：这里既说"用哪个模型"，也存"这个模型是什么"（窗口/输出/模态/档位）。
// 为什么不单开一层存模型：读配置的时候必须能离线算出上下文预算，
// 而"再问一次上游"在接口挂掉时什么也拿不到 —— 那就不该是运行时的依赖。
//
// 上游的数字从两条路进来：
//   - 选模型：下拉列表带能力，选中即填（所以新建时不用手抄窗口）
//   - 跟随上游：开着就每轮对话前再问一次，适合背后会换模型的接口
//
// 能力四项（窗口/输出/模态/档位）是**必填**的：留空等于"不知道"，而"以后再拉"
// 还是同一个结果。上游没报就让人填一个，别用默认值装作知道。
export default function ModelForm({ v, narrow, top }) {
  const editP = v.editP
  if (!editP) return null
  const isNew = !!editP._new
  const set = (k, val) => v.setEditP({ ...editP, [k]: val })
  const tools = (editP.tools || '').split(',').map(x => x.trim()).filter(Boolean)
  const lst = (v.modelList || {})[editP.endpoint] || null
  const found = (lst && lst.models) || []
  const cur = found.find(m => m.id === editP.model) || null
  const hasImage = (editP.input || []).includes('image')
  const toggleImage = () => {
    const cur2 = editP.input || ['text']
    set('input', hasImage ? cur2.filter(x => x !== 'image') : [...new Set([...cur2, 'image'])])
  }
  // 选中（或手打到）一个列表里存在的模型 → 把它的能力抄进来
  const onModel = (val) => {
    const m = found.find(x => x.id === val)
    if (!m) { set('model', val); return }
    v.setEditP({
      ...editP, model: val,
      context_window: m.context_window || editP.context_window || '',
      max_tokens_out: m.max_tokens || editP.max_tokens_out || '',
      input: m.input && m.input.length ? m.input : (editP.input && editP.input.length ? editP.input : ['text']),
      effort_levels: m.effort_levels && m.effort_levels.length ? m.effort_levels : (editP.effort_levels || [])
    })
  }
  // 用上游现在的值覆盖这四项（不选中也能按，比如手写过 id 之后）
  const pullCaps = () => {
    if (!cur) return
    v.setEditP({
      ...editP,
      context_window: cur.context_window || editP.context_window || '',
      max_tokens_out: cur.max_tokens || editP.max_tokens_out || '',
      input: cur.input && cur.input.length ? cur.input : editP.input,
      effort_levels: cur.effort_levels && cur.effort_levels.length ? cur.effort_levels : editP.effort_levels
    })
  }
  const levels = (editP.effort_levels || []).length
    ? ['none', ...editP.effort_levels.filter(x => x !== 'none')]
    : ['none', 'low', 'medium', 'high', 'max']
  const listHint = !lst ? '还没拉过 · 点 ⟳ 或聚焦输入框'
    : lst.loading ? '正在问上游…'
    : lst.error ? ('拉不到：' + lst.error + (lst.at ? '（下面是上次见到的）' : ''))
    : `${found.length} 个模型${lst.at ? ' · ' + agoText(lst.at) : ''}`
  return (
    <Card kind="col-detail" top={top}
      title={isNew ? '新建配置' : ('配置 · ' + editP.name)}
      dirty={v.formDirty}
      onClose={() => v.closeOne()}
      actions={!isNew
        ? <button className="btn danger del-top" onClick={v.deleteProfile}>删除</button>
        : null}>
      <div className="card-bd">
        <Section>接入</Section>
        <Field label="名称"><input value={editP.name || ''} onChange={e => set('name', e.target.value)} /></Field>
        <Field label="接口" hint="地址和凭据归接口那层；同一个接口可以被多套配置共用">
          <select value={editP.endpoint || ''} onChange={e => {
            set('endpoint', e.target.value)
            v.loadModelList(e.target.value)
          }}>
            <option value="">（未选择）</option>
            {(v.endpoints || []).map(ep => (
              <option key={ep.name} value={ep.name}>{ep.name}</option>
            ))}
          </select>
        </Field>
        <Field label="模型"
          hint="可以直接输入上游没报的模型名；从列表里选会自动填下面的能力">
          <div className="row-btn">
            <input list="agentd-model-ids" value={editP.model || ''} placeholder="qwen3.8-flash-next-iq3_xxs"
              onFocus={() => { if (!lst) v.loadModelList(editP.endpoint) }}
              onChange={e => onModel(e.target.value)} />
            <button type="button" className="btn" title="刷新上游列表"
              onClick={() => v.loadModelList(editP.endpoint, true)}>⟳</button>
          </div>
          <datalist id="agentd-model-ids">
            {found.map(m => <option key={m.id} value={m.id}>{m.name || ''}</option>)}
          </datalist>
          <span className={'field-hint' + (lst && lst.error ? ' warn' : '')}>{listHint}</span>
        </Field>

        <Section>能力</Section>
        <div className="row-2">
          <Field label="窗口大小 (tokens)" hint={cur && cur.context_window ? '上游报的：' + cur.context_window : '上游没报就自己填'}>
            <input type="number" min="1" value={editP.context_window || ''} placeholder="131072"
              onChange={e => set('context_window', e.target.value)} />
          </Field>
          <Field label="输出上限 (tokens)" hint="留空按 8192">
            <input type="number" min="0" value={editP.max_tokens_out || ''} placeholder="8192"
              onChange={e => set('max_tokens_out', e.target.value)} />
          </Field>
        </div>
        <SwitchRow label="能接收图像" hint={hasImage ? '会话里可以贴图 / 用截图工具' : '只能处理文字；界面上会拦下图像输入'}
          checked={hasImage} onChange={toggleImage} />
        <TagInput label="支持的思考强度" value={editP.effort_levels || []}
          suggestions={cur && cur.effort_levels ? cur.effort_levels : null}
          onChange={list => set('effort_levels', list)}
          hint="agentd 就按这些档位渲染；留空表示不限制，界面列出全部五档" />
        <SwitchRow label="跟随上游" hint="每轮对话前重新问一次这个模型的能力（适合背后会换模型的接口）；问不到就用上面存的值"
          checked={!!editP.auto_caps} onChange={x => set('auto_caps', x)} />
        {cur ? (
          <div className="set-meta">
            上游现在说：{cur.context_window ? Math.round(cur.context_window / 1024) + 'k 窗口' : '没报窗口'}
            {cur.max_tokens ? ' · 输出 ' + cur.max_tokens : ''}
            {(cur.input || []).includes('image') ? ' · 图像' : ' · 纯文字'}
            {(cur.effort_levels || []).length ? ' · 强度 ' + cur.effort_levels.join('/') : ''}
            {'　'}
            <button type="button" className="btn tiny" onClick={pullCaps}>用上游的值</button>
          </div>
        ) : null}

        <Section>运行</Section>
        <Field label="默认思考强度" hint="这里存的是默认值；会话里可以临时改，不落盘">
          <select value={editP.reasoning_effort || 'none'}
            onChange={e => set('reasoning_effort', e.target.value)}>
            {(levels.includes(editP.reasoning_effort || 'none') ? levels : [...levels, editP.reasoning_effort])
              .map(x => <option key={x} value={x}>{x}</option>)}
          </select>
        </Field>
        <Field label="输入上限 (tokens)" hint="单次请求的实际预算，留空按窗口的 80%">
          <input type="number" min="0" max="1000000" placeholder="自动"
            value={editP.max_prompt_tokens || ''}
            onChange={e => set('max_prompt_tokens', Number(e.target.value) || 0)} />
        </Field>
        <Field label="压缩阈值 (%)" hint="用到上限的百分之几开始摘要/裁剪，留空按 80">
          <input type="number" min="1" max="99" placeholder="80"
            value={editP.compact_pct || ''}
            onChange={e => set('compact_pct', Number(e.target.value) || 0)} />
        </Field>

        <Section>可用工具</Section>
        <ChoiceGrid
          label="勾选这个模型能调用的工具"
          options={(v.toolList || []).map(t => ({ value: t.name, label: t.name, hint: t.description || '' }))}
          selected={tools}
          onToggle={name => set('tools', tools.includes(name)
            ? tools.filter(x => x !== name).join(',')
            : [...tools, name].join(','))}
          hint="内置与外置都在这里统一开关" />

        <Section>提示词</Section>
        <LinkRow label="system" value={editP.system} onClick={v.openSystemEditor} />

        {!isNew ? (
          <div className="card-foot del-bottom-wrap">
            <button className="btn danger del-bottom" onClick={v.deleteProfile}>删除配置</button>
          </div>
        ) : null}
      </div>
    </Card>
  )
}

// 多久之前：状态行永远说清"这份列表是什么时候的"，缓存就不会悄悄变旧
function agoText(at) {
  const t = new Date(at).getTime()
  if (!t) return ''
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 90) return '刚刚'
  if (s < 3600) return Math.round(s / 60) + ' 分钟前'
  if (s < 86400) return Math.round(s / 3600) + ' 小时前'
  return Math.round(s / 86400) + ' 天前'
}
