import React from 'react'

// 渲染期抛错时 React 18 会卸载整棵树 —— 没有边界就是白屏，连"刷新"都点不到。
// 这里兜住，把错误给用户看，并留一个重新加载的按钮。
// 注意：边界只接渲染/生命周期里的同步错误，接不到事件回调和异步错误（那些走 window.onerror）。
export default class ErrorBoundary extends React.Component {
  state = { err: null }

  static getDerivedStateFromError(err) {
    return { err }
  }

  componentDidCatch(err, info) {
    console.error('[xaihi] 渲染出错', err, info && info.componentStack)
  }

  render() {
    const { err } = this.state
    if (!err) return this.props.children
    const msg = String((err && err.message) || err || '未知错误')
    const stack = String((err && err.stack) || '').split('\n').slice(0, 6).join('\n')
    return (
      <div className="crash">
        <h2>界面出错了</h2>
        <p className="crash-msg">{msg}</p>
        <p className="crash-hint">后端还在跑，刷新一般就能恢复。如果反复出现，把下面这段贴给我。</p>
        {stack ? <pre className="crash-stack">{stack}</pre> : null}
        <button className="btn" onClick={() => location.reload()}>重新加载</button>
      </div>
    )
  }
}
