import React from 'react'
import Card from './Card'

// 工具详情 / 插件表单卡（第二级）
export default function ToolCard({ v, narrow, profiles, top }) {
  const cfg = v.toolCfg
  const form = v.toolForm
  if (!cfg && !form) return null
  return (
    <Card kind="col-detail" top={top}
      title={form ? (form.isNew ? 'new plugin' : 'tool · ' + form.name) : ('tool · ' + cfg.name)}
      onClose={() => (form ? v.closeToolForm() : v.closeOne())}
      actions={form ? (
        <span className="card-actions title-actions">
          <button className="btn" onClick={v.closeToolForm}>取消</button>
          <button className="btn" onClick={v.savePlugin}>保存</button>
        </span>
      ) : null}>

      {form ? (
        <div className="card-bd">
          <label className="field"><span>名称（小写字母开头，a-z 0-9 _）</span>
            <input value={form.name} disabled={!form.isNew}
              onChange={e => v.setToolForm({ ...form, name: e.target.value })} /></label>
          <label className="field"><span>说明（模型看到的描述，也可在三级卡里改）</span>
            <input value={form.description}
              onChange={e => v.setToolForm({ ...form, description: e.target.value })} /></label>
          <label className="field"><span>命令（每行一个参数，直接 exec 不走 shell）</span>
            <textarea className="plugin-cmd" value={form.command}
              placeholder={'~/Workspace/agentd/scripts/tools/kb_get\n{{id}}'}
              onChange={e => v.setToolForm({ ...form, command: e.target.value })} /></label>
          <label className="field"><span>超时（毫秒）</span>
            <input type="number" min="1000" value={form.timeoutMs}
              onChange={e => v.setToolForm({ ...form, timeoutMs: e.target.value })} /></label>
          <div className="item static">
            <span className="item-title">常驻进程（JSON-RPC over stdio）</span>
            <button type="button" role="switch" aria-checked={!!form.persistent}
              className={'tg' + (form.persistent ? ' on' : '')}
              onClick={() => v.setToolForm({ ...form, persistent: !form.persistent })}><b /></button>
          </div>
          <label className="field"><span>参数（JSON Schema properties）</span>
            <textarea className="plugin-json" value={form.paramsText}
              onChange={e => v.setToolForm({ ...form, paramsText: e.target.value })} /></label>
          <label className="field"><span>必填参数（逗号分隔）</span>
            <input value={form.requiredText}
              onChange={e => v.setToolForm({ ...form, requiredText: e.target.value })} /></label>
          {!form.isNew ? (
            <div className="card-foot del-bottom-wrap" style={{ display: 'flex' }}>
              <button className="btn danger del-bottom" onClick={v.deletePlugin}>删除插件</button>
            </div>
          ) : null}
        </div>
      ) : null}

      {!form ? (
        <div className="card-bd">
          <div className="item" onClick={() => v.openToolEditor(cfg.name, cfg.description)}>
            <span className="item-title">说明</span>
            <span className="item-desc">{cfg.description || '（空）'}</span>
          </div>
          <div className="set-section">来源</div>
          <div className="item static">
            <span className="item-title">{cfg.source === 'plugin' ? '外置插件' : '内置工具'}</span>
            <span className="item-desc">{cfg.source === 'plugin' ? '清单 + 脚本，改完即生效' : 'Go 内置，改需重编译'}</span>
          </div>
          {cfg.source === 'plugin' ? (
            <>
              <div className="field"><span>清单</span>
                <div className="tool-meta">{cfg.path}</div></div>
              <div className="field"><span>超时</span>
                <div className="tool-meta">{cfg.timeout_ms ? cfg.timeout_ms + ' ms' : '默认（20s）'}
                  {cfg.persistent ? ' · 常驻进程（JSON-RPC over stdio）' : ' · 一次性进程'}</div></div>
            </>
          ) : null}

          <div className="set-section">启用情况</div>
          <div className="item static">
            <span className="item-title">由 profile 决定</span>
          </div>
          <div className="set-meta">
            {(() => {
              const on = (profiles || []).filter(pr => (pr.tools || []).includes(cfg.name)).map(pr => pr.name)
              return on.length ? '在 ' + on.join('、') + ' 中启用' : '当前没有任何 profile 启用它'
            })()}
          </div>
          <div className="set-meta">要改可用性：打开对应模型的「工具」勾选</div>
        </div>
      ) : null}
    </Card>
  )
}
