import React from 'react'
import DirtyActions from './DirtyActions'
import Card from './Card'

const fmtTime = (s) => {
  if (!s) return ''
  const d = new Date(s)
  if (isNaN(d)) return s
  const p2 = (n) => String(n).padStart(2, '0')
  return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`
}

// 一级卡：常规配置 + 模型列表 + 工具列表 + 任务
export default function ListCard({ v, narrow, profiles, uiCfg, openConv, top }) {
  return (
    <Card kind="col-list" top={top} hdClass="list-hd" title="SETTINGS / PROFILES" closeTitle="退出设置"
      onClose={() => v.closeOne()} actions={<DirtyActions v={v} dirty={v.settingsDirty} />}>
      <div className="card-bd">
        <div className="set-section">常规配置</div>
        <div className="set-card">
          <div className="item static">
            <span className="item-title">工具调用轮次上限</span>
            <button type="button" role="switch" aria-checked={!!v.settings.tool_rounds_enabled}
              className={'tg' + (v.settings.tool_rounds_enabled ? ' on' : '')}
              onClick={() => v.setSettings({ ...v.settings, tool_rounds_enabled: !v.settings.tool_rounds_enabled })}><b /></button>
          </div>
          <div className="set-meta">开启该项以避免工具循环调用</div>
          {v.settings.tool_rounds_enabled ? (
            <label className="field" style={{ marginTop: '.5rem' }}><span>轮次上限</span>
              <input type="number" min="1" max="1000" value={v.settings.tool_rounds || 8}
                onChange={e => v.setSettings({ ...v.settings, tool_rounds: Number(e.target.value) || 8 })} /></label>
          ) : null}
        </div>

        <div className="set-card">
          <div className="item static">
            <span className="item-title">自动归档</span>
            <button type="button" role="switch" aria-checked={!!v.settings.auto_archive_enabled}
              className={'tg' + (v.settings.auto_archive_enabled ? ' on' : '')}
              onClick={() => v.setSettings({ ...v.settings, auto_archive_enabled: !v.settings.auto_archive_enabled })}><b /></button>
          </div>
          <div className="set-meta">超过设定天数未活跃的会话自动归入归档</div>
          {v.settings.auto_archive_enabled ? (
            <label className="field" style={{ marginTop: '.5rem' }}><span>未活跃天数</span>
              <input type="number" min="1" max="3650" value={v.settings.auto_archive_days || 7}
                onChange={e => v.setSettings({ ...v.settings, auto_archive_days: Number(e.target.value) || 7 })} /></label>
          ) : null}
        </div>

        <div className="set-card">
          <div className="item static">
            <span className="item-title">界面 · 运行中提示</span>
          </div>
          <div className="set-meta">运行中状态栏轮播的句子，一行一条；存在本机 localStorage（不进后端）</div>
          <textarea className="ui-quips" key={uiCfg ? 'cfg' : 'none'} defaultValue={(() => { try { const a = JSON.parse(localStorage.getItem('ui.quips') || '[]'); const list = (Array.isArray(a) && a.length) ? a : (uiCfg?.quips || []); return list.join('\n') } catch { return (uiCfg?.quips || []).join('\n') } })()}
            onChange={e => { const arr = e.target.value.split('\n').map(x => x.trim()).filter(Boolean); try { localStorage.setItem('ui.quips', JSON.stringify(arr)) } catch {} }} />
          <div className="set-meta">改完刷新页面生效；留空则回到 ui.json 的默认值</div>
        </div>

        <div className="set-card">
          <div className="item static">
            <span className="item-title">截图清理</span>
            <button type="button" role="switch" aria-checked={!!v.settings.shots_auto_clean}
              className={'tg' + (v.settings.shots_auto_clean ? ' on' : '')}
              onClick={() => v.setSettings({ ...v.settings, shots_auto_clean: !v.settings.shots_auto_clean })}><b /></button>
          </div>
          <div className="set-meta">超过保留天数的截图文件自动删除（数据库里的消息不受影响）</div>
          {v.settings.shots_auto_clean ? (
            <label className="field" style={{ marginTop: '.5rem' }}><span>保留天数</span>
              <input type="number" min="1" max="3650" value={v.settings.shots_keep_days || 7}
                onChange={e => v.setSettings({ ...v.settings, shots_keep_days: Number(e.target.value) || 7 })} /></label>
          ) : null}
          <div className="item static" style={{ marginTop: '.4rem' }}>
            <span className="item-desc" style={{ margin: 0 }}>{v.shotInfo || '—'}</span>
            <button className="btn" onClick={v.cleanShots}>立即清理</button>
          </div>
        </div>

        <div className="set-section">模型配置</div>
        <div className="set-card">
          {profiles.map(pr => (
            <div className={'item col' + (v.editP && v.editP.name === pr.name ? ' on' : '')} key={pr.name}
              onClick={() => v.openProfile(pr)}>
              <b className="item-title">{pr.name}</b>
              <span className="item-desc">{pr.model || '（未设置模型）'}</span>
            </div>
          ))}
          <div className="item item-new" onClick={v.newProfile}>＋ 新建 profile</div>
        </div>

        <div className="set-section">工具配置</div>
        <div className="set-card">
          {[...v.toolList].sort((a, b) => {
            const g = (x) => (x.source === 'plugin' ? 1 : 0)   // 内置 0，外置 1
            const d = g(a) - g(b)
            return d !== 0 ? d : a.name.localeCompare(b.name)
          }).map(t => (
            <div className="item" key={t.name} title={t.path || '内置工具（在程序内指定）'}
              onClick={() => v.pickTool(t)}>
              <b className="item-title">{t.name}
                {t.source !== 'plugin' ? <span className="src-tag builtin">内置</span> : null}
              </b>
              <span className="item-desc">{t.description}</span>
            </div>
          ))}
          <div className="item item-new" onClick={v.newPlugin}>＋ 新建插件</div>

          <div className="set-section">任务{v.taskActive ? '（' + v.taskActive + ' 个在跑）' : ''}</div>
          <div className="set-card">
            {v.tasks.length === 0 ? (
              <div className="set-meta">还没有任务{v.recipes.length ? '（可用配方：' + v.recipes.join('、') + '）' : '（未注册配方）'}</div>
            ) : v.tasks.map(t => (
              <div className="item col" key={t.id} onClick={() => v.pickTask(t)}>
                <b className="item-title">#{t.id} {t.kind}
                  <span className={'src-tag ' + (t.status === 'done' ? 'builtin' : 'plugin')}>{t.status}</span>
                </b>
                <span className="item-desc">{t.child_conv ? '子会话 ↳#' + t.child_conv + ' · ' : ''}{t.error || fmtTime(t.created_at)}</span>
              </div>
            ))}
            {v.taskDetail ? (
              <div className="item static" style={{ display: 'block' }}>
                {(v.taskDetail.steps || []).map(s => (
                  <div key={s.seq} className="task-step">
                    <span className={'ts-dot ' + s.status} />
                    <span className="ts-cmd">{s.cmd || s.name}</span>
                    <span className="ts-meta">{s.exit_code ? 'exit ' + s.exit_code : ''} {s.ms}ms</span>
                    {s.output ? <pre className="ts-out">{s.output.slice(0, 300)}</pre> : null}
                  </div>
                ))}
                {v.taskDetail.task && v.taskDetail.task.child_conv ? (
                  <button className="btn" style={{ marginRight: '.4rem' }}
                    onClick={() => { openConv(String(v.taskDetail.task.child_conv)); v.setShowSet(false) }}>
                    打开子会话 No.{String(v.taskDetail.task.child_conv).padStart(4, '0')}</button>
                ) : null}
                {v.taskDetail.task && v.taskDetail.task.status === 'interrupted' ? (
                  <button className="btn" onClick={async () => {
                    await fetch('/api/tasks/' + v.taskDetail.task.id + '/restore', { method: 'POST' })
                    v.setTaskDetail(null); v.loadTasks()
                  }}>执行收尾</button>
                ) : null}
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </Card>
  )
}
