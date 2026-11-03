import React from 'react'
import XIcon from './XIcon'

// 保存前的改动清单。
// 改动都攒在前端（草稿），这里摊开给人看一眼再统一提交 ——
// 一次保存可能包含好几个 profile、工具说明和常规配置，
// 不列出来就直接写后端，用户不知道"刚才那一下到底存了什么"。
export default function SaveSheet({ v }) {
  const list = v.sheet
  if (!list) return null
  const rows = list.reduce((n, c) => n + c.rows.length, 0)
  return (
    <div className="sheet-scrim"
      onClick={e => { if (e.target === e.currentTarget) v.setSheet(null) }}>
      <div className="sheet" role="dialog" aria-modal="true" aria-label="待保存的改动">
        <div className="sheet-hd">
          <span className="ef-label"><span className="ef-label-t">待保存的改动</span></span>
          <span style={{ flex: 1 }} />
          <button className="icon-x" onClick={() => v.setSheet(null)} aria-label="关闭" title="关闭"><XIcon /></button>
        </div>
        <div className="sheet-bd">
          {list.map((c, i) => (
            <div className="sheet-grp" key={i}>
              <div className="sheet-grp-t">{c.title}</div>
              {c.rows.map((r, k) => (
                <div className="sheet-row" key={k}>
                  <span className="sheet-k">{r.label}</span>
                  <span className="sheet-from" title={r.from}>{r.from}</span>
                  <span className="sheet-ar">→</span>
                  <span className="sheet-to" title={r.to}>{r.to}</span>
                </div>
              ))}
            </div>
          ))}
        </div>
        <div className="sheet-ft">
          <span className="sheet-sum">{list.length} 项 · {rows} 处改动</span>
          <span style={{ flex: 1 }} />
          <button className="btn" onClick={v.discardAll}>全部放弃</button>
          <button className="btn save" onClick={v.commitSheet} disabled={v.committing}>
            {v.committing ? '保存中…' : '确认保存'}
          </button>
        </div>
      </div>
    </div>
  )
}
