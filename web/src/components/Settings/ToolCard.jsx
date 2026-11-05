import React from 'react'
import Card from './Card'
import { Section, Field, InfoField, SwitchRow } from './form'

// 工具卡（第二级：详细配置）
//
// 和模型配置一样，点开就是**可编辑的表单**，没有"详情态 ↔ 编辑态"的切换。
// 内置工具也走同一张表单：命令、超时、常驻、参数这些由程序决定，
// 用 .ro 修饰类灰掉（同一个输入框，只是不能点）；
// 说明两边都能改 —— 它是长文本，点那一行进 3 级卡编辑。
//
// 这样两种工具的字段顺序、间距、标签位置完全一致，
// 差别只剩"哪些格子能点"，而不是两套长得不一样的界面。
export default function ToolCard({ v, narrow, profiles, top }) {
  const form = v.toolForm
  if (!form) return null
  const editable = form.isNew || form.source === 'plugin'
  const ro = editable ? '' : ' ro'
  const set = (k, val) => v.setToolForm({ ...form, [k]: val })

  return (
    <Card kind="col-detail" top={top}
      title={form.isNew ? 'new plugin' : ('tool · ' + form.name)}
      dirty={v.toolFormDirty}
      onClose={() => v.closeToolForm()}>

      <div className="card-bd">
        {/* 这里不套 Section：「说明」两个字既是分组名又是条目名，会连着出现两遍 */}
        <div className="item" onClick={() => v.openToolEditor(form.name, form.description)}>
          <span className="item-title">说明</span>
          <span className="item-desc">{form.description || '（空）'}</span>
        </div>

        <Section>来源与运行</Section>
        <InfoField label="来源"
          value={editable
            ? '外置插件 · 清单 + 脚本，改完即生效'
            : '内置工具 · Go 内置，改需重编译'} />
        <Field label="名称" hint={editable ? '小写字母开头，可用 a-z 0-9 _；改名后所有 profile 里的引用会自动跟着换' : '内置工具的名称由程序决定'}>
          <input className={ro} value={form.name} disabled={!editable}
            onChange={e => set('name', e.target.value)} />
        </Field>
        <Field label="命令" hint={editable ? '每行一个参数，直接 exec 不走 shell' : '内置工具的命令写在 Go 代码里'}>
          <textarea className={'plugin-cmd' + ro} value={form.command} disabled={!editable}
            placeholder={editable ? '~/Workspace/agentd/scripts/tools/kb_get\n{{id}}' : '（内置实现，没有命令行）'}
            onChange={e => set('command', e.target.value)} />
        </Field>
        <Field label="超时（毫秒）" hint={editable ? '留空按 20000' : '由实现决定（bash 180s、recall 300ms，其余跟随会话）'}>
          <input className={ro} type="number" min="0" value={form.timeoutMs} disabled={!editable}
            placeholder={editable ? '20000' : '由实现决定'}
            onChange={e => set('timeoutMs', e.target.value)} />
        </Field>
        <SwitchRow label="常驻进程" disabled={!editable}
          hint={editable
            ? '开启后按 JSON-RPC over stdio 长连接，适合每次启动都很慢的脚本'
            : '内置工具都是一次性进程'}
          checked={!!form.persistent}
          onChange={x => { if (editable) set('persistent', x) }} />

        {editable ? (
          <>
            <Section>参数</Section>
            <Field label="JSON Schema properties">
              <textarea className="plugin-json" value={form.paramsText}
                onChange={e => set('paramsText', e.target.value)} />
            </Field>
            <Field label="必填参数" hint="逗号分隔，必须出现在上面的 properties 里">
              <input value={form.requiredText}
                onChange={e => set('requiredText', e.target.value)} />
            </Field>
          </>
        ) : null}

        <Section>清单</Section>
        <InfoField label="文件" mono
          value={form.path || '（内置，没有清单文件）'} />

        <Section>启用情况</Section>
        <div className="item static">
          <span className="item-title">由 profile 决定</span>
        </div>
        <div className="set-meta">
          {(() => {
            const on = (profiles || []).filter(pr => (pr.tools || []).includes(form.name)).map(pr => pr.name)
            return on.length ? '在 ' + on.join('、') + ' 中启用' : '当前没有任何 profile 启用它'
          })()}
        </div>
        <div className="set-meta">要改可用性：打开对应模型的「工具」勾选</div>

        {editable && !form.isNew ? (
          <div className="set-foot-actions">
            <span style={{ flex: 1 }} />
            <button className="btn danger" onClick={v.deletePlugin}>删除插件</button>
          </div>
        ) : null}
      </div>
    </Card>
  )
}
