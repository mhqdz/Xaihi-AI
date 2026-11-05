import React from 'react'
import Card from './Card'
import { Section, Field, LinkRow, ChoiceGrid } from './form'

// 模型详情 / 新建模型（第二级：详细配置）
//
// 十来个字段原来是一路平铺下来的，没有分段，找某一项只能整列扫。
// 按"接进来 → 用哪个模型 → 上下文预算 → 能用什么工具 → 提示词"分成五段，
// 每段的标签也只留短语，解释挪到字段下面那行小字里。
export default function ModelForm({ v, narrow, top }) {
  const editP = v.editP
  if (!editP) return null
  const isNew = !!editP._new
  const set = (k, val) => v.setEditP({ ...editP, [k]: val })
  const tools = (editP.tools || '').split(',').map(x => x.trim()).filter(Boolean)
  return (
    <Card kind="col-detail" top={top}
      title={isNew ? '新建模型' : ('模型详情 · ' + editP.name)}
      dirty={v.formDirty}
      onClose={() => v.closeOne()}
      actions={!isNew
        ? <button className="btn danger del-top" onClick={v.deleteProfile}>删除</button>
        : null}>
      <div className="card-bd">
        <Section>接入</Section>
        <Field label="名称"><input value={editP.name || ''} onChange={e => set('name', e.target.value)} /></Field>
        <Field label="模型" hint="模型本身（窗口、模态、上游 id）在「模型」那层维护，这里只挑用哪个">
          <select value={editP.model || ''} onChange={e => set('model', e.target.value)}>
            <option value="">（未选择）</option>
            {(v.models || []).map(m => (
              <option key={m.name} value={m.name}>{m.id} · {m.endpoint}</option>
            ))}
          </select>
        </Field>

        <Section>运行</Section>
        <Field label="默认思考强度" hint="这里存的是默认值；会话里可以临时改，不落盘">
          <select value={editP.reasoning_effort || 'none'}
            onChange={e => set('reasoning_effort', e.target.value)}>
            {['none', 'low', 'medium', 'high', 'max'].map(x => <option key={x} value={x}>{x}</option>)}
          </select>
        </Field>
        <Field label="输入上限 (tokens)" hint="单次请求的实际预算，留空按 105000">
          <input type="number" min="0" max="1000000" placeholder="105000"
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
