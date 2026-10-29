import React, { useMemo } from 'react'
import { marked } from 'marked'
import DOMPurify from 'dompurify'

marked.setOptions({ gfm: true, breaks: true })

const pad = n => String(n).padStart(2, '0')

// Markdown 渲染（模型输出不可信 → 一律过 DOMPurify）
export function Md({ text }) {
  const html = useMemo(() => DOMPurify.sanitize(marked.parse(String(text || ''))), [text])
  return <div className="prose ef-prose md" dangerouslySetInnerHTML={{ __html: html }} />
}

// 把回复按 markdown 标题切成"01 / 标题"编号段落；无标题则整段输出
export function Sections({ text }) {
  const lines = String(text || '').split('\n')
  const out = []
  let buf = [], n = 0
  const flush = () => {
    if (buf.length) {
      const body = buf.join('\n').trim()
      if (body) out.push(<Md text={body} key={'b' + out.length} />)
      buf = []
    }
  }
  for (const raw of lines) {
    const m = /^#{1,4}\s+(.*)$/.exec(raw.trim())
    if (m) {
      flush(); n++
      out.push(
        <div className="sec" key={'h' + n}>
          <span className="secno">{pad(n)}</span>
          <span className="secti">{m[1]}</span>
        </div>)
    } else buf.push(raw)
  }
  flush()
  if (out.length === 0) return <Md text={text} />
  return <>{out}</>
}
