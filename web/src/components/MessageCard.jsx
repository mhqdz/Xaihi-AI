import React from 'react'
import { Sections } from './Markdown'

const no3 = n => 'No.' + String(n).padStart(3, '0')

// 用户 / Agent 气泡（纯展示）
// props 全是算好的结果：组件内部不读全局 msgs / 不读其他 state，否则 memo 永远失效
function MessageCard({ m, seq, vidx, onCollapse }) {
  if (typeof window !== 'undefined') (window.__renders = window.__renders || { msg: 0, tool: 0, note: 0 }).msg++
  const isUser = m.role === 'user'
  const rms = m.reasoning_ms || (m.rsecs ? m.rsecs * 1000 : 0)
  return (
    <div data-vidx={vidx} className={'msg ef-panel cut-tl ' + m.role}>
      <div>
        <div className="mhead">
          <span className={'av' + (isUser ? ' u' : '')}>{isUser ? '用' : '◈'}</span>
          <b>{isUser ? '用户' : 'Agent'}</b>
          {m.interject ? <span className="ef-tag">插话</span> : null}
          {seq ? <span className="ef-tag">{no3(seq)}</span> : null}
          <span className="ef-field">{m.at || ''}</span>
          {!isUser && m.content ? (
            <span className="mact" onClick={() => navigator.clipboard?.writeText(m.content)}>复制</span>
          ) : null}
        </div>
        {m.reasoning ? (
          <details className="fold">
            <summary className="think-sum">思考过程 · <span className="num">{String(m.reasoning).length}</span> 字{
              rms ? <>{' · '}<span className="num">{(rms / 1000).toFixed(1)}</span>{'s'}</> : null}</summary>
            <pre className="think">{m.reasoning}</pre>
            <button className="fold-close" onClick={e => {
              const d = e.currentTarget.closest('details')
              if (d) d.open = false
              // 收起后回到这条消息的开头（比"思考过程"那一行再高一点）
              const box = d && d.closest('.msg')
              if (box) onCollapse && onCollapse(box)
            }}>
              <span className="arw"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 15l6-6 6 6" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square"/></svg></span>
              收起思考
            </button>
          </details>
        ) : null}
        {m.content ? <Sections text={m.content} /> : null}
        {(m.images || []).map((u, k) => <img key={k} className="msg-img" src={u} alt="image" />)}
      </div>
    </div>
  )
}

// 已完成消息对象不可变 ⇒ 同一引用即可跳过渲染（Step 2 的关键）
export default React.memo(MessageCard, (a, b) =>
  a.m === b.m && a.seq === b.seq && a.vidx === b.vidx && a.onCollapse === b.onCollapse
)
