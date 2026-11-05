import React from 'react'
import XIcon from './XIcon'

// 设置面板里所有卡片共用的外壳：同一个标题栏结构（返回 × + 标签 + 右侧动作）
// kind：col-editor（3 级）| col-detail（2 级：模型/工具）| col-list（1 级）
// 二级卡只有 kind 相同、内容不同 —— 容器/头/间距/滚动都不允许各写一套。
// dirty：这张卡里有还没提交的改动。
//
// 保存统一在一级卡（窄屏是底部操作条），所以二三级上没有保存按钮 ——
// 但"我改了没改"是另一件事，属于状态而不是动作：光标停在长文本里改了几个字，
// 眼睛不会跑到最右边那栏去看有没有冒出按钮。标题后一个点，所有层级同一套信号。
export default function Card({ kind, top, title, onClose, closeTitle = '关闭', hdClass = '', actions, dirty, children }) {
  return (
    <div className={'card ' + kind + (top ? ' is-top' : '')}>
      <div className={'card-hd' + (hdClass ? ' ' + hdClass : '')}>
        <button className="icon-x back card-back narrow-only" onClick={onClose} aria-label={closeTitle} title={closeTitle}><XIcon /></button>
        <span className="ef-label"><span className="ef-label-t">{title}</span></span>
        {dirty ? <span className="dirty-dot" title="有未保存的改动" aria-label="有未保存的改动" /> : null}
        <span style={{ flex: 1 }} />
        {actions}
      </div>
      {children}
    </div>
  )
}
