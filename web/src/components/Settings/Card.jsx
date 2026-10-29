import React from 'react'
import XIcon from './XIcon'

// 设置面板里所有卡片共用的外壳：同一个标题栏结构（返回 × + 标签 + 右侧动作）
// kind：col-editor（3 级）| col-detail（2 级：模型/工具）| col-list（1 级）
// 二级卡只有 kind 相同、内容不同 —— 容器/头/间距/滚动都不允许各写一套。
export default function Card({ kind, top, title, onClose, closeTitle = '关闭', hdClass = '', actions, children }) {
  return (
    <div className={'card ' + kind + (top ? ' is-top' : '')}>
      <div className={'card-hd' + (hdClass ? ' ' + hdClass : '')}>
        <button className="icon-x back card-back narrow-only" onClick={onClose} aria-label={closeTitle} title={closeTitle}><XIcon /></button>
        <span className="ef-label"><span className="ef-label-t">{title}</span></span>
        <span style={{ flex: 1 }} />
        {actions}
      </div>
      {children}
    </div>
  )
}
