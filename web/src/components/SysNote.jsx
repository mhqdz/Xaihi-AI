import React from 'react'

const no3 = n => 'No.' + String(n).padStart(3, '0')

// 系统提示（定时唤醒 / 交接 / 外部写入）：一行淡字 + 细线
function SysNote({ m, seq }) {
  if (typeof window !== 'undefined') (window.__renders = window.__renders || { msg: 0, tool: 0, note: 0 }).note++
  return (
    <div className="sysnote">
      {seq ? <span className="sysnote-no">[No.{String(seq).padStart(3, '0')}]</span> : null}
      <span className="sysnote-t">{m.content}</span>
      <span className="sysnote-line" />
    </div>
  )
}

export default React.memo(SysNote, (a, b) => a.m === b.m && a.seq === b.seq)
