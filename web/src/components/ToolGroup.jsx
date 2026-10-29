import React from 'react'

const no3 = n => 'No.' + String(n).padStart(3, '0')

// 一行工具：可读化的参数 + 耗时/字数
function rowOf(m) {
  let args = ''
  if (m.toolArgs) { try { args = JSON.stringify(JSON.parse(String(m.toolArgs))) } catch { args = String(m.toolArgs) } }
  const meta = [m.ms ? (m.ms < 1000 ? m.ms + 'ms' : (m.ms / 1000).toFixed(1) + 's') : '',
    m.content ? String(m.content).length + ' 字' : ''].filter(Boolean).join(' · ')
  return { args, meta }
}

// 工具组（默认收拢）：组内第一条负责 Tool 头，展开后逐行列出该轮调用
// meta = { start, first, last, stats, think }：由父层算好，同一份 msgs 下引用不变
function ToolGroup({ m, seq, meta, open, onToggle }) {
  if (typeof window !== 'undefined') (window.__renders = window.__renders || { msg: 0, tool: 0, note: 0 }).tool++
  if (!meta) return null
  const { start, first, last, stats, think } = meta
  if (!first && !open) return null
  const { args, meta: rmeta } = rowOf(m)
  return (
    <div className={'toolnote' + (first ? ' run-start' : '') + ((last || !open) ? ' run-end' : '')}>
      {first ? <span className="tn-c tl" /> : null}
      {(last || !open) ? <span className="tn-c br" /> : null}
      {first ? (
        <div className="tn-hd" onClick={() => onToggle(start)} role="button" title={open ? '收起' : '展开'}>
          <span className="tn-tag">Tool</span>
          <span className="tn-fold">{open ? '▾' : '▸'}</span>
          <span className="tn-sp" />
          <span className="tn-stats">{stats.from ? (stats.to && stats.to !== stats.from ? no3(stats.from) + '–' + String(stats.to).padStart(3, '0') : no3(stats.from)) : ''}{stats.thinks ? ' · 思考 ' + stats.thinks : ''} · {stats.n}{stats.chars ? ' · ' + (stats.chars >= 1000 ? (stats.chars / 1000).toFixed(1) + 'k' : stats.chars) + ' 字' : ''}</span>
        </div>
      ) : null}
      {open ? (
        <>
          {think ? (
            <details className="tn-think">
              <summary>思考 · {think.length} 字</summary>
              <pre className="think">{think}</pre>
            </details>
          ) : null}
          <div className="tn-row">
            <span className="tn-no">{seq ? no3(seq) : '··'}</span>
            <span className="tn-name">{m.toolName || '工具'}</span>
            <span className="tn-meta">{rmeta}</span>
          </div>
          {args ? <div className="tn-arg" title={args}>{args}</div> : null}
          {(m.images || []).map((u, k2) => <img key={k2} className="msg-img" src={u} alt="tool" />)}
          {(m.content || '').trim() ? (
            <details className="tn-out">
              <summary>输出</summary>
              <pre className="out">{m.content}</pre>
            </details>
          ) : null}
        </>
      ) : null}
    </div>
  )
}

const sameStats = (x, y) => x === y || (!!x && !!y &&
  x.n === y.n && x.chars === y.chars && x.ms === y.ms && x.thinks === y.thinks && x.from === y.from && x.to === y.to)

// meta 对象在 msgs 变化时会重建，但字段没变就不必重渲染 ⇒ 逐字段比较
export default React.memo(ToolGroup, (a, b) =>
  a.m === b.m && a.seq === b.seq && a.open === b.open && a.onToggle === b.onToggle &&
  (a.meta === b.meta || (!!a.meta && !!b.meta &&
    a.meta.start === b.meta.start && a.meta.first === b.meta.first &&
    a.meta.last === b.meta.last && a.meta.think === b.meta.think &&
    sameStats(a.meta.stats, b.meta.stats)))
)
