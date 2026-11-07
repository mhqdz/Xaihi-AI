import React from 'react'

// 配置类卡片的共用骨架。
//
// 三种 2 级面板（模型详情 / 工具详情 / 插件表单）原来各写一套字段：
// 同样是"标签 + 控件"，有的用 <label className="field"><span>，
// 有的用 .set-f，间距和标签位置都在漂，改一处另一处跟不上。
// 统一到这几个小组件之后，前端不再解释字段语义（哪个是地址、哪个是路径），
// 只负责"标签 + 控件 + 提示"这一层排版。
//
// 层级语义（由 Panel 决定，这里只是被它们复用）：
//   1 级 ListCard   通用配置 + 详细配置入口
//   2 级 本文件     详细配置
//   3 级 EditorCard 长文本输入（system 提示 / 工具说明）

// 分组标题：小号字 + 左侧竖条 + 右侧延伸细线
export function Section({ children }) {
  return <div className="set-section">{children}</div>
}

// 可编辑字段：标签在上、控件在下，可选一行说明
export function Field({ label, hint, children }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
      {hint ? <span className="field-hint">{hint}</span> : null}
    </label>
  )
}

// 只读信息：不带框的一行文字。
//
// 曾经套的是 .tool-meta（灰底 + 边线的块）—— 那是消息流里"工具输出"的样式，
// 放进表单读起来就是一个灰色输入框：看着能改、点上去没反应。
// 面板里的框是有语义的（白框=能改，.ro 灰框=不能改的输入框），
// 纯信息就不该有框；只有 .mono 用来强调"这是一段路径 / 标识"。
export function InfoField({ label, value, mono }) {
  return (
    <div className="field">
      <span>{label}</span>
      <div className={'ro-val' + (mono ? ' mono' : '')}>{value}</div>
    </div>
  )
}

// 开关行。整行可点：那个 38×24 的钮在窄屏上并不好按，
// 而这一行的语义就是"切换它"，点哪儿都算同一件事。
export function SwitchRow({ label, hint, checked, onChange, disabled }) {
  const flip = () => { if (!disabled) onChange(!checked) }
  return (
    <div className={'item static switch-row' + (disabled ? ' ro' : '')} onClick={flip}>
      <span className="item-title">{label}</span>
      <button type="button" role="switch" aria-checked={checked} disabled={disabled}
        className={'tg' + (checked ? ' on' : '')}
        onClick={e => { e.stopPropagation(); flip() }}><b /></button>
      {hint ? <div className="set-meta">{hint}</div> : null}
    </div>
  )
}

// 进 3 级卡片的入口：长文本不适合塞在 2 级里编辑。
//
// 它长得像输入框（白框 + 右侧 ›），因为内容确实能改，只是改在下一层；
// 只读信息（InfoField）没有框。两者外观的区别就是"能不能改"。
export function LinkRow({ label, value, onClick, placeholder = '（空）' }) {
  return (
    <div className="item link-row" onClick={onClick}>
      <span className="item-title">{label}</span>
      <span className="caret" aria-hidden="true">›</span>
      <span className="item-desc">{value || placeholder}</span>
    </div>
  )
}

// 多选：两列等宽，勾选态由 .choice.on 表达
export function ChoiceGrid({ label, options, selected, onToggle, hint }) {
  return (
    <div className="field">
      <span>{label}</span>
      <div className="choices">
        {options.map(o => {
          const on = selected.includes(o.value)
          return (
            <label key={o.value} title={o.hint || ''} className={'choice' + (on ? ' on' : '')}>
              <input type="checkbox" checked={on} onChange={() => onToggle(o.value)} />
              <span>{o.label}</span>
            </label>
          )
        })}
      </div>
      {hint ? <span className="field-hint">{hint}</span> : null}
    </div>
  )
}
