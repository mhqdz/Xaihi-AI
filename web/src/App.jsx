import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import NumberFlow from '@number-flow/react'
import { stream, useLiveMsg } from './conversation/streamStore'
import { isGhostMsg as isGhostM, neighborRole, runStart, runStats, thinkBefore } from './conversation/messageGroups'
import { Sections } from './components/Markdown'
import MessageCard from './components/MessageCard'
import SysNote from './components/SysNote'
import ToolGroup from './components/ToolGroup'

// 实时气泡：订阅 streamStore，只它随 delta 重渲染（历史消息不参与）
function LiveBubble({ busy }) {
  const live = useLiveMsg()
  const has = live && ((live.content || '').trim() || (live.reasoning || '').trim())
  if (!has && !busy) return null
  return (
    <div className="msg ef-panel cut-tl assistant">
      <div>
        <div className="mhead">
          <span className="av">◈</span>
          <b>Agent</b>
          <span className="ef-field">生成中…</span>
        </div>
        {(live && (live.reasoning || '').trim()) ? (
          <details className="fold" open>
            <summary className="think-sum">思考过程 · <span className="num">{live.reasoning.length}</span> 字{
              live.rsecs ? <>{' · '}<span className="num">{live.rsecs.toFixed(1)}</span>{'s'}</> : null}</summary>
            <pre className="think">{live.reasoning}</pre>
          </details>
        ) : null}
        {(live && (live.content || '').trim()) ? <Sections text={live.content} /> : <div className="live-wait">等待模型响应…</div>}
      </div>
    </div>
  )
}

// 日历式翻数：数字变化时逐位滚动（+1 向上，-1 向下）
function Roll({ value, digits = 2 }) {
  const txt = String(value ?? 0).padStart(digits, '0')
  const prevRef = useRef(txt)
  const prev = prevRef.current.length === txt.length ? prevRef.current : txt
  const changed = Number(txt) !== Number(prev)
  const up = Number(txt) > Number(prev)
  useEffect(() => { prevRef.current = txt }, [txt])
  return (
    <b className="roll">
      {txt.split('').map((d, i) => {
        if (!changed || prev[i] === d) return <span className="rw" key={'s' + i + d}>{d}</span>
        return (
          <span className="rw" key={'r' + i + txt}>
            <span className={'rd ' + (up ? 'up' : 'down')}><i>{d}</i></span>
          </span>
        )
      })}
    </b>
  )
}

// 数字格：number-flow 管逐位滚动；外层 figIn 管入场
// 关键在于**不重挂载**（重挂载会让 number-flow 丢失旧值 → 不滚）
// 用 class 重播动画（remove → 强制回流 → add）
function FigCell({ value }) {
  const box = useRef(null)
  const prev = useRef(value)
  useEffect(() => {
    if (prev.current === value) return
    const el = box.current
    if (el) {
      el.classList.remove('figIn')
      void el.offsetWidth
      el.classList.add('figIn')
    }
    prev.current = value
  }, [value])
  return (
    <span className="nfwrap" ref={box}>
      <NumberFlow className="nf" value={value} format={{ minimumIntegerDigits: 2 }} />
    </span>
  )
}

const api = (p, opt) => fetch(p, opt).then(r => r.json())
const pad = n => String(n).padStart(2, '0')
const now = () => new Date().toTimeString().slice(0, 5)

// 字符数简写：12.3k / 240k
const fmtK = (n) => n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1) + 'k' : String(n)

// 最后活跃时间：今天显示 HH:MM，否则 MM.DD
const fmtActive = (iso) => {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d)) return String(iso).slice(5, 10).replace('-', '.')
  const t = new Date()
  const sameDay = d.toDateString() === t.toDateString()
  const pad = n => String(n).padStart(2, '0')
  return sameDay ? pad(d.getHours()) + ':' + pad(d.getMinutes())
                 : pad(d.getMonth() + 1) + '.' + pad(d.getDate())
}



function ProfileSeg({ profiles, value, onChange, small }) {
  return (
    <div className="ef-seg" role="radiogroup" aria-label="profile" style={small ? { height: 'auto' } : undefined}>
      {profiles.map(p => (
        <label key={p.name} title={p.model}>
          <input type="radio" name={'seg-' + (small ? 's' : 'l')} checked={p.name === value}
            onChange={() => onChange(p.name)} />
          <span>{p.name}</span>
        </label>
      ))}
    </div>
  )
}

// 历史消息归一：把 assistant.tool_calls 与其后的 tool 结果合成 tools 数组，
// 这样刷新后"思考过程"和"工具调用记录"不会消失（之前只存在于流式时的前端状态 ✗）
function normalize(arr) {
  // 工具结果保留为独立消息（有自己的 seq，界面上是单独一个气泡）
  const call = {}
  for (const m of arr) {
    for (const tc of (m.tool_calls || [])) {
      call[tc.id] = { name: tc.function?.name || '工具', args: tc.function?.arguments || '' }
    }
  }
  return arr.filter(m => !(m.role === 'user' && (m.images || []).length && /^（上一个工具的截图/.test(m.content || '')))
    .map(m => {
    if (m.role === 'tool') {
      const c = call[m.tool_call_id] || {}
      return { ...m, toolName: c.name, toolArgs: c.args, ms: m.tool_ms || 0 }
    }
    const { tools, ...rest } = m    // assistant 不再内嵌工具列表
    return rest
  })
}


function EditorCol({ value, onChange, onDone, onCancel, placeholder }) {
  const ta = useRef(null)
  useEffect(() => {
    const el = ta.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = (el.scrollHeight + 2) + 'px'   // 新样式表无 !important，内联生效 ✓
  }, [value])
  return (
    <div className={"card col-editor" + ((editField || toolEdit) ? " is-top" : "")}>
      <textarea ref={ta} className="set-area" value={value || ''} placeholder={placeholder}
        onChange={e => onChange(e.target.value)} />
      <div className="set-col-foot">
        <span className="set-meta" style={{ marginRight: 'auto' }}>{(value || '').length} 字</span>

      </div>
    </div>
  )
}

// 执行中计时器（只在自己内部 re-render，不带动整页）
function Elapsed({ t0 }) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 500)
    return () => clearInterval(t)
  }, [])
  const s = Math.max(0, (now - t0) / 1000)
  return <span className="ef-field">{s.toFixed(1)}s</span>
}

// 把回复按 markdown 标题切成"01 / 标题"编号段落；无标题则整段输出
// （Sections / Md 已移到 components/Markdown.jsx）

export default function App() {
  const [convs, setConvs] = useState([])
  const [archived, setArchived] = useState([])
  const [profiles, setProfiles] = useState([])
  const [profile, setProfile] = useState(localStorage.getItem('profile') || '180-think')
  const [tab, setTab] = useState('live')
  // 窄屏默认收起左栏（它是浮层，展开会盖住正文）；宽屏沿用上次状态
  const [sideOpen, setSideOpen] = useState(() => {
    if (window.innerWidth < 1400) return false
    return localStorage.getItem('sideOpen') !== '0'
  })
  const [sideW, setSideW] = useState(() => Number(localStorage.getItem('sideW')) || 250)
  const [colW, setColW] = useState(() => Number(localStorage.getItem('colW')) || 960)
  const colDrag = useRef(null)
  const stageRef = useRef(null)
  const setOpenRef = useRef(false)
  const threadRef = useRef(null)
  const stickRef = useRef(true)   // 是否跟随到底：只有用户没上翻过才跟随
  // 是否贴着底部（新内容是否跟随）——之前漏了定义，finish 时会 ReferenceError
  const isAtBottom = (th = threadRef.current, tol = 40) =>
    !!th && (th.scrollHeight - th.scrollTop - th.clientHeight) <= tol
  const [inputH, setInputH] = useState(() => Number(localStorage.getItem('inputH')) || (typeof window !== 'undefined' && window.innerWidth < 1400 ? 52 : 92))
  const hDrag = useRef(null)
  const abortRef = useRef(null)
  const [aborted, setAborted] = useState(false)
  const [showSet, setShowSet] = useState(new URLSearchParams(location.search).has('settings'))
  const [editP, setEditP] = useState(null)
  const [toolList, setToolList] = useState([])
  const [settings, setSettings] = useState({ tool_rounds_enabled: true, tool_rounds: 8 })
  const [toolEdit, setToolEdit] = useState(null)
  const [toolCfg, setToolCfg] = useState(null)   // 工具配置卡（只读信息）
  const [toolForm, setToolForm] = useState(null) // 插件表单（新建/编辑）
  const [tasks, setTasks] = useState([])         // 交接任务
  const [pendingTools, setPendingTools] = useState([])
  const [openRuns, setOpenRuns] = useState(() => new Set())   // 已展开的工具组（默认全收拢）  // 正在执行的工具（tool_start 到 tool 之间）
  const [recipes, setRecipes] = useState([])
  const [taskActive, setTaskActive] = useState(0)
  const [taskDetail, setTaskDetail] = useState(null)
  const [expandedTool, setExpandedTool] = useState(null)
  const [models, setModels] = useState({ loading: false, list: [], err: '' })
  const [readingIdx, setReadingIdx] = useState(0)
  const [peek, setPeek] = useState({ open: false, idx: -1, top: 0 })
  const [shotInfo, setShotInfo] = useState('')
  const convIdRef = useRef(null)   // 当前会话 id，用于丢弃过期的续流请求
  const [narrow, setNarrow] = useState(typeof window !== 'undefined' && window.innerWidth < 1400)
  const [ctx, setCtx] = useState(null)   // 上下文占用 {chars,max,pct,raw}
  const [tps, setTps] = useState(0)   // 上游实时速度（tok/s，来自 usage 事件）
  const loadTasks = async () => {
    const r = await api('/api/tasks').catch(() => null)
    if (r && !Array.isArray(r)) { setTasks(r.tasks || []); setRecipes(r.recipes || []); setTaskActive(r.active || 0) }
  }
  const atBottomRef = useRef(true)   // 是否贴在底部（决定新内容是否跟随）
  const suppressPeekClose = useRef(false)
  const suppressTimer = useRef(null)
  const [editField, setEditField] = useState(null)
  const [editOriginal, setEditOriginal] = useState('')
  const [editSnap, setEditSnap] = useState('')
  const [setSnap, setSetSnap] = useState(() => JSON.stringify({ tool_rounds_enabled: true, tool_rounds: 8 }))

  // 快照去掉内部标记 _new，避免"一打开就算脏"✗
  const snapOf = (p) => { if (!p) return ''; const { _new, ...rest } = p; return JSON.stringify(rest) }
  const formDirty = !!editP && snapOf(editP) !== editSnap
  const editorDirty = (editField || toolEdit)
    ? ((editField ? (editP?.[editField] || '') : (toolEdit?.description || '')) !== (editOriginal || ''))
    : false
  const settingsDirty = JSON.stringify(settings) !== setSnap
  const anyDirty = formDirty || editorDirty || settingsDirty
  const drag = useRef(null)
  const [convId, setConvId] = useState(null)
  const [title, setTitle] = useState('新会话')
  const [msgs, setMsgs] = useState([])
  const visible = msgs.filter(m => m.role !== 'tool')   // 工具结果不计入轮次 ✓
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [status, setStatus] = useState('')
  const [stage, setStage] = useState('')   // compacting | generating | ''
  const [secs, setSecs] = useState(null)
  const endRef = useRef(null)

  useEffect(() => { localStorage.setItem('sideW', String(sideW)) }, [sideW])

  useEffect(() => { localStorage.setItem('colW', String(colW)) }, [colW])

  useEffect(() => { localStorage.setItem('inputH', String(inputH)) }, [inputH])
  // 竖屏：输入框默认更矮（宽屏保持原高度；切到竖屏时把过高的收回来）
  useEffect(() => {
    if (narrow) setInputH(h => (h > 52 ? 52 : h))
  }, [narrow])

  // 右上角小三角：拖动调整输入框高度（替代原生右下角 resize ✓）
  const startResize = (e) => {
    e.preventDefault(); e.stopPropagation()
    hDrag.current = { y: e.clientY, h: inputH }
    const move = (ev) => {
      const h = Math.min(420, Math.max(56, hDrag.current.h - (ev.clientY - hDrag.current.y)))
      setInputH(h)
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.style.cursor = ''; document.body.style.userSelect = ''
    }
    document.body.style.cursor = 'ns-resize'; document.body.style.userSelect = 'none'
    window.addEventListener('mousemove', move); window.addEventListener('mouseup', up)
  }

  // 插话：把内容压进后端队列，模型在下一个工具轮次就能看到（不中断当前生成）
  const interject = async () => {
    // 先确认后端真的在跑；不在跑就按普通发送走（否则只会存成插话、不会唤起新一轮）
    const run = await api('/api/running').catch(() => [])
    if (!(run || []).includes(String(convId))) { return send() }
    const t = text.trim()
    if (!t) return
    if (!convId) { setStatus('这一轮还没拿到会话 id，稍等一下再插话'); return }
    const r = await fetch('/api/conv/' + convId + '/interject', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text: t }),
    })
    if (!r.ok) { setStatus('✗ 插话失败'); return }
    setText('')
    setMsgs(m => [...m, { role: 'user', content: t, at: now(), interject: true }])
    setStatus('已插话')
  }

  // 停止生成：中断 fetch → 后端 r.Context() 被取消 → 上游请求结束
  const stop = () => {
    if (convId) fetch('/api/conv/' + convId + '/stop', { method: 'POST' }).catch(() => {})
    abortRef.current?.abort()
    abortRef.current = null
    setAborted(true)
    setStatus('已停止（已保留已生成的内容）')
  }

  // 滚轮：在输入区/状态栏上滚动也归给会话区（否则滚轮在这里"失灵"）
  // 只在输入区/状态栏上帮会话滚（不挂在 .stage 上，避免误伤设置面板等子层 ✓）
  const onComposerWheel = (e) => {
    const th = stageRef.current?.querySelector('.thread')
    if (!th) return
    th.scrollTop += e.deltaY
  }

  // 取装饰线的实际横坐标（.col 左缘再往左 1.4rem）
  const lineX = () => {
    const col = stageRef.current?.querySelector('.thread .col')
    return col ? col.getBoundingClientRect().left - 22 : -999
  }

  // 鼠标靠近：按距离设置不透明度（0 → 1，无生硬切换）
  const onStageMove = (e) => {
    const stage = stageRef.current
    const col = stage?.querySelector('.thread .col')
    if (!stage || !col) return
    const cr = col.getBoundingClientRect(), sr = stage.getBoundingClientRect()
    const dx = Math.abs(e.clientX - (cr.left - 22))
    const a = Math.max(0, Math.min(1, 1 - (dx - 6) / 54))     // 横向：近线才显示
    stage.style.setProperty('--lineA', String(a))
    stage.style.setProperty('--lineX', (cr.left - 22 - sr.left) + 'px')
    stage.style.setProperty('--lineY', (e.clientY - sr.top) + 'px')   // 纵向：可见窗口跟随鼠标
    stage.style.cursor = dx <= 10 ? 'col-resize' : ''
  }

  const onStageLeave = () => {
    if (stageRef.current) {
      stageRef.current.style.setProperty('--lineA', '0')
      stageRef.current.style.cursor = ''
    }
  }

  // 只在贴近线时才允许拖动
  const onStageDown = (e) => {
    if (Math.abs(e.clientX - lineX()) <= 10) startColDrag(e)
  }

  // 拖动阅读栏左边界调整正文宽度（居中 → 左右留白同步变化）
  const startColDrag = (e) => {
    e.preventDefault()
    colDrag.current = { x: e.clientX, w: colW }
    const move = (ev) => {
      const dx = colDrag.current.x - ev.clientX
      const w = Math.min(1400, Math.max(560, colDrag.current.w + dx * 2))
      setColW(w)
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // 拖动抽屉右缘调整宽度（200 ~ 520）
  const startDrag = (e) => {
    e.preventDefault()
    drag.current = { x: e.clientX, w: sideW }
    const move = (ev) => {
      const w = Math.min(520, Math.max(200, drag.current.w + (ev.clientX - drag.current.x)))
      setSideW(w)
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // 逐级退出：编辑器 → 详情 → 设置；有改动先问是否保存
  // 文本域随内容长高（写 CSS 变量，避免被 !important 压掉）
  const autoGrow = (el) => {
    if (!el) return
    el.style.height = 'auto'
    let h = el.scrollHeight + 2
    if (el.classList.contains('set-area')) {
      // 上限来自卡片自身的 max-height（由 CSS 决定，宽窄屏一致），减去标题与字数栏
      // 可用高度 = 容器内容区高度 − 卡片上下留白（padding）− 卡片标题/字数栏 − 边框
      const card = el.closest('.card')
      let cap = Infinity
      if (card && card.parentElement) {
        const box = card.parentElement
        const cs = getComputedStyle(box)
        const inner = box.clientHeight - parseFloat(cs.paddingTop || 0) - parseFloat(cs.paddingBottom || 0)
        const hd = card.querySelector('.card-hd')
        const ft = card.querySelector('.set-col-foot')
        cap = inner - (hd ? hd.offsetHeight : 0) - (ft ? ft.offsetHeight : 0) - 4
      }
      el.style.overflowY = h > cap ? 'auto' : 'hidden'
      h = Math.min(h, Math.max(160, cap))
    }
    el.style.setProperty('--h', h + 'px')
    el.style.height = h + 'px'
  }

  // 阅读位置：滚到哪条消息，就显示第几条
  const onThreadScroll = () => {
    const box = threadRef.current
    if (!box) return
    const top = box.getBoundingClientRect().top
    const bottom = top + box.clientHeight
    let idx = 0
    box.querySelectorAll('[data-vidx]').forEach(el => {
      if (el.getBoundingClientRect().top < bottom) idx = Number(el.dataset.vidx) || idx
    })
    setReadingIdx(idx)
    {
      const th = threadRef.current
      // 严格判定"在底部"：留 6px 容差；用户一旦上翻就不再跟随（直到自己滚回底部）
      if (th) {
        const at = th.scrollHeight - th.scrollTop - th.clientHeight <= 8
        atBottomRef.current = at
        stickRef.current = at   // 用户滚回底部才重新跟随；上翻即解除
      }
    }
    if (suppressPeekClose.current) {
      clearTimeout(suppressTimer.current)
      suppressTimer.current = setTimeout(() => { suppressPeekClose.current = false }, 260)
    } else {
      setPeek(p => (p.open ? { open: false, idx: -1, top: 0 } : p))
    }
  }


  // 缩略图导航：悬停 36/36 → 右侧栏左侧弹出 5 条，中心对应当前阅读位置
  // 实时消息（还没落库）没有 seq：用 maxSeq+序号 给个临时编号，保证列表编号连续
  // 系统级提示（工具轮次上限 / 已停止 / 中断 / 空转中止）：模型要看到，但不该像对话那样占编号
  const isSysNote = (m) => !(m.reasoning || '').trim() && (m.origin === 'timer' || m.origin === 'handoff' || (m.role === 'assistant' &&
    /^\s*(——)?\s*（(已达到工具轮次上限|已停止|中断|检测到连续|任务回报|任务已派发)/.test(m.content || '')))

  const fmtTime = (s) => {
    if (!s) return ''
    const d = new Date(s)
    if (isNaN(d)) return s
    const p2 = (n) => String(n).padStart(2, '0')
    return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`
  }

  const maxSeq = msgs.reduce((a, m) => Math.max(a, m.seq || 0), 0)   // 含工具消息，避免编号撞号
  const liveNo = new Map()
  {
    let n = maxSeq
    for (const m of visible) if (!m.seq) liveNo.set(m, ++n)
  }
  // 阅读位置编号（data-vidx）：visible 下标 +1（空正文 assistant 也占号，与历史行为一致）
  const vidxOf = new Map()
  visible.forEach((m, i) => vidxOf.set(m, i + 1))

  const idxOfSeq = (seq) => visible.findIndex(m => (m.seq || 0) === seq)
  const curIdx = () => {
    return Math.max(0, Math.min(visible.length - 1, (readingIdx || 1) - 1))
  }
  const peekRows = (() => {
    if (!peek.open || !visible.length) return []
    const center = peek.idx >= 0 ? peek.idx : curIdx()
    const out = []
    for (let d = -2; d <= 2; d++) {
      const i = center + d
      if (i >= 0 && i < visible.length) out.push({ i, m: visible[i] })
    }
    return out
  })()
  const scrollToMsg = (i) => {
    const el = document.querySelector('[data-vidx="' + (i + 1) + '"]')
    if (!el) return
    suppressPeekClose.current = true
    clearTimeout(suppressTimer.current)
    suppressTimer.current = setTimeout(() => { suppressPeekClose.current = false }, 600)
    el.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }

  const roundMax = visible.length
  const roundNow = readingIdx || visible.length

  const closeOne = async () => {
    const open = editField || toolEdit
    if (open) {
      const cur = editField ? (editP?.[editField] || '') : (toolEdit?.description || '')
      if (cur !== (editOriginal || '')) {
        if (!confirm('文本已修改，是否保存？\n确定 = 保存并关闭　取消 = 放弃修改')) {
          if (editField) setEditP({ ...editP, [editField]: editOriginal })
          else setToolEdit({ ...toolEdit, description: editOriginal })
        } else if (toolEdit) {
          await fetch('/api/tools/' + toolEdit.name, { method: 'PUT', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ description: toolEdit.description }) })
          refresh()
        }
      }
      if (editField) setEditField(null); else setToolEdit(null)
      return
    }
    if (editP) { setEditP(null); return }
    setShowSet(false)
  }

  const refresh = async () => {
    setConvs(await api('/api/convs'))
    setArchived(await api('/api/convs?archived=1').catch(() => []))
    setProfiles(await api('/api/profiles'))
    setToolList(await api('/api/tools').catch(() => []))
    const st = await api('/api/settings').catch(() => ({ tool_rounds_enabled: true, tool_rounds: 8 }))
    setSettings(st); setSetSnap(JSON.stringify(st))
  }
  useEffect(() => {
    refresh().then(async () => {
      const q = new URLSearchParams(location.search)
      const c = q.get('conv')                                     // ?conv=<id> 深链
      if (c) openConv(c)
      const pn = q.get('profile')                                 // ?settings=1&profile=9-exec 直接打开编辑
      if (pn) {
        const list = await api('/api/profiles')
        const pr = (list || []).find(x => x.name === pn)
        if (pr) {
          setEditP({ ...pr, tools: (pr.tools || []).join(',') })
          const fld = q.get('field')                 // ?field=system 直接打开提示编辑
          if (fld) { setEditOriginal(pr[fld] || ''); setEditField(fld) }
        }
      }
      const tq = q.get('task')                     // ?task=3 直接看任务详情
      if (tq) {
          const d = await api('/api/tasks/' + tq).catch(() => null)
          if (d && d.task) {
            setTaskDetail(d)
            // 详情在列表卡深处：等渲染后滚过去（否则深链打开等于没开）
            setTimeout(() => { const el = document.querySelector('.task-step'); if (el) el.scrollIntoView({ block: 'center' }) }, 350)
          }
        }
      const tl = q.get('tool')                    // ?tool=kb_search 直接打开工具卡
      if (tl) {
        const t = (await api('/api/tools').catch(() => []))?.find(x => x.name === tl)
        if (t) setToolCfg({ ...t, enabled: true, subtools: [] })
        if (t && q.get('tooledit')) { setEditOriginal(t.description || ''); setToolEdit({ name: t.name, description: t.description }) }
      }
        if (q.get('newplugin')) setToolForm({ isNew: true, name: '', description: '', command: '', timeoutMs: 20000, persistent: false, paramsText: '{\n  "query": { "type": "string" }\n}', requiredText: 'query' })
    })
  }, [])
  useEffect(() => { setOpenRef.current = showSet }, [showSet])
  useEffect(() => {
    const el = document.querySelector('.col-editor .set-area')
    if (el) setTimeout(() => autoGrow(el), 0)
  }, [editField, toolEdit])
  useEffect(() => { loadShotStats() }, [])
  useEffect(() => { loadTasks(); const t = setInterval(loadTasks, 5000); return () => clearInterval(t) }, [])
  useEffect(() => {
    const on = () => setNarrow(window.innerWidth < 1400)
    window.addEventListener('resize', on)
    return () => window.removeEventListener('resize', on)
  }, [])
  useEffect(() => { localStorage.setItem('profile', profile) }, [profile])
  useEffect(() => { if (window.innerWidth >= 1400) localStorage.setItem('sideOpen', sideOpen ? '1' : '0') }, [sideOpen])
  // 运行中的趣味提示（大肥鱼语录）：不打扰，只做背景信息
  const [quip, setQuip] = useState('')
  const [uiCfg, setUiCfg] = useState(null)
  useEffect(() => {
    // 前端配置：web/public/ui.json（静态资源，不经后端；改完刷新即可）
    fetch('/ui.json').then(r => r.ok ? r.json() : null).then(c => c && setUiCfg(c)).catch(() => {})
  }, [])
  useEffect(() => {
    let local = []
    try { local = JSON.parse(localStorage.getItem('ui.quips') || '[]') } catch { local = [] }
    const lines = local.length ? local : (uiCfg && Array.isArray(uiCfg.quips) && uiCfg.quips.length) ? uiCfg.quips : [
      '「我是吃白饭的大肥鱼」',
      '「DeepSleep...」',
    ]
    if (!busy) { setQuip(''); return }
    let i = 0
    setQuip(lines[0])
    const t = setInterval(() => { i = (i + 1) % lines.length; setQuip(lines[i]) }, 10000)
    return () => clearInterval(t)
  }, [busy, uiCfg])

  // 跟随到底：只有 stickRef 为真（用户没上翻过 / 已滚回底部）才跟随
  useEffect(() => {
    const th = threadRef.current
    if (!th || !stickRef.current) return
    const pin = () => { if (stickRef.current) th.scrollTop = th.scrollHeight }
    pin()
    requestAnimationFrame(pin)
  }, [msgs, status])

  // 运行中每帧校正（流式增量不一定触发 effect）；用户上翻后 stickRef=false，立刻停手
  useEffect(() => {
    if (!busy) return
    let raf = 0
    const tick = () => {
      const th = threadRef.current
      if (th && stickRef.current) th.scrollTop = th.scrollHeight
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [busy])

  // 续流去重：DB 里已落库的那条（本轮进行中的 assistant）不该与流式副本并存
  const trimDup = (list, live) => {
    const out = [...list]
    while (out.length) {
      const last = out[out.length - 1]
      const c = last && last.role === "assistant" ? (last.content || "") : null
      if (c !== null && (c === "" || (live.content || "").startsWith(c) || /——（中断/.test(c))) out.pop()
      else break
    }
    return out
  }

  // 滚到会话底部（新消息/进入会话时用）
  const scrollToBottom = () => {
    stickRef.current = true
    const stick = () => { const th = threadRef.current; if (th) th.scrollTop = th.scrollHeight }
    requestAnimationFrame(stick)
    setTimeout(stick, 60)
    setTimeout(stick, 250)   // 图片/字体撑开高度后再校正一次
    setTimeout(stick, 800)
  }

  // 截图占用统计（设置卡里显示）
  // 上下文占用：当前会发给模型的字符数 / 上限（还有未压缩的原始字符数）
  const loadCtx = async (id) => {
    if (!id) { setCtx(null); return }
    const r = await api('/api/context?conv=' + id + '&profile=' + encodeURIComponent(profile)).catch(() => null)
    setCtx(r && !r.error ? r : null)
  }

  const loadShotStats = async () => {
    const r = await api('/api/shots/stats').catch(() => null)
    if (r) setShotInfo(`当前 ${r.count} 张，占用 ${r.size_mb.toFixed(1)} MB`)
  }

  const openConv = async (id) => {
    stream.clear()   // 丢掉上一个会话残留的实时缓冲（结束/切换时不能留"生成中…"幽灵气泡）
    convIdRef.current = id
    setConvId(id)
    // 竖屏：抽屉是浮层，选完会话要收起来（否则遮住正文）
    if (typeof window !== 'undefined' && window.innerWidth < 1400) setSideOpen(false)
    // A：会话 id 进 URL，刷新后能回到同一会话（也便于分享/重开）
    if (id) history.replaceState(null, '', '?conv=' + id)
    else history.replaceState(null, '', location.pathname)
    if (!id) { setMsgs([]); setTitle('新会话'); return }
    const c = await api('/api/conv/' + id)
    setMsgs(normalize(c.messages || [])); setTitle(c.title || '会话')
    scrollToBottom()   // 进入会话定位到最新消息
      loadCtx(id)
    // 后台仍在跑？轮询直到结束（刷新页面后能"接上"）
    // 单一事实来源：直接请求续传流。没内容/已结束会立刻返回，不会挂着
    // 只有真的在跑才去接续传流；否则后端会回 404（控制台里那条红字），纯噪声
    const runningNow = ((await api('/api/running').catch(() => [])) || []).includes(String(id))
    if (runningNow) attachStream(id)
    else setBusy(false)
  }


  // D：SSE 断点续传 —— 刷新后从后端缓冲继续接收（不在前端存状态）
  // D：SSE 断点续传 —— 后端还在跑就一直跟着；断流/404 时重连，绝不"假装结束"
  const attachStream = async (id) => {
    const sleep = (ms) => new Promise(r => setTimeout(r, ms))
    const running = async () => ((await api('/api/running').catch(() => [])) || []).includes(String(id))
    let live = { role: 'assistant', content: '', reasoning: '', at: now() }
    let pushTimer = null
    let done = false
    const push = () => {
      if (pushTimer || done) return
      pushTimer = setTimeout(() => { pushTimer = null; if (!done) stream.set({ ...live }) }, 80)
    }
    const finish = async (msg) => {
      setPendingTools([])
      if (done) return
      done = true
      const cc = await api('/api/conv/' + id).catch(() => null)
      if (cc && convIdRef.current === id) {
        setMsgs(normalize(cc.messages || []))
        if (isAtBottom()) scrollToBottom()   // 用户没在底部就别把他拽回去
      }
      if (pushTimer) { clearTimeout(pushTimer); pushTimer = null }
      stream.clear()
      setSyncing(false); setBusy(false); setStatus(msg || '已同步'); refresh()
    }
    for (let attempt = 0; attempt < 60 && !done; attempt++) {
      if (convIdRef.current !== id) return
      let resp = null
      try {
        resp = await fetch('/api/conv/' + id + '/stream?from=0')
      } catch (e) {
        if (convIdRef.current !== id) return
        await sleep(800); continue              // 网络抖动 / 连接被断 → 重连
      }
      if (!resp.ok) {
        if (await running()) { await sleep(900); continue }   // 还没注册或刚结束 → 再试
        await finish('已同步'); return
      }
      const reader = resp.body.getReader(); const dec = new TextDecoder()
      let buf = ''
      setBusy(true)   // 刷新后仍能反映"运行中"（状态从后端流推断）
      const guard = setInterval(async () => { if (!done && !(await running())) finish('已同步') }, 2500)
      try {
        for (;;) {
          const r = await reader.read(); if (r.done) break
          buf += dec.decode(r.value, { stream: true })
          let i
          while ((i = buf.indexOf('\n\n')) >= 0) {
            const block = buf.slice(0, i); buf = buf.slice(i + 2)
            let ev = '', data = ''
            for (const line of block.split('\n')) {
              if (line.startsWith('event: ')) ev = line.slice(7)
              else if (line.startsWith('data: ')) data = line.slice(6)
            }
            let d = {}; try { d = JSON.parse(data) } catch { }
            if (ev === 'reasoning') { live.reasoning += d.text || ''; push() }
            else if (ev === 'delta') { live.content += d.text || ''; push() }
            else if (ev === 'tool_start') {
              setPendingTools(p => [...p, { id: Math.random().toString(36).slice(2), name: d.name, args: d.args, t0: Date.now() }])
            }
            else if (ev === 'tool') {
              setPendingTools(p => { const key = d.name + '|' + (d.args || ''); const k = p.findIndex(x => x.name + '|' + (x.args || '') === key); return k >= 0 ? p.filter((_, idx) => idx !== k) : p.slice(1) })
              const doneLive = { ...live }
              live = { role: 'assistant', content: '', reasoning: '', tools: [], at: now() }
              setMsgs(m => [...m.filter(x => !x.__live), doneLive,
                { role: 'tool', toolName: d.name, toolArgs: d.args, content: d.text || '', ms: d.ms || 0, at: now(), __liveTool: true }])
              stream.clear()
            }
              else if (ev === 'usage') { if (d.tps > 0) setTps(d.tps) }
          }
        }
      } catch (e) {
        /* 读流中断：下面按运行状态决定重连还是收尾 */
      } finally { clearInterval(guard) }
      if (convIdRef.current !== id) return
      if (await running()) { await sleep(600); continue }     // 服务端提前关流 → 重连
      await finish('已同步'); return
    }
    await finish('已同步')
  }

  const send = async () => {
    const t = text.trim(); if (!t || busy) return
    setText('')
    sendText(t)
  }

  const sendText = async (t) => {
    if (!t || busy) return; setBusy(true); setStatus(''); setSecs(null); setAborted(false)
    const ac = new AbortController()
    abortRef.current = ac
    setMsgs(m => [...m, { role: 'user', content: t, at: now() }])
    scrollToBottom()   // 发送后回到最新位置
    let cur = convId
    let ended = false        // 收流后置 true：节流里没落地的 push 不许再改写缓冲
    let pushTimer = null
    try {
      const resp = await fetch('/api/chat', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ conv_id: cur || '', text: t, profile }),
        signal: ac.signal,
      })
      const reader = resp.body.getReader(); const dec = new TextDecoder()
      let buf = '', asst = { role: 'assistant', content: '', reasoning: '', at: now() }
      const push = () => {
        if (pushTimer || ended) return
        pushTimer = setTimeout(() => { pushTimer = null; if (!ended) stream.set({ ...asst }) }, 80)
      }
      for (;;) {
        const { value, done } = await reader.read(); if (done) break
        buf += dec.decode(value, { stream: true })
        let i
        while ((i = buf.indexOf('\n\n')) >= 0) {
          const block = buf.slice(0, i); buf = buf.slice(i + 2)
          let ev = '', data = ''
          for (const line of block.split('\n')) {
            if (line.startsWith('event: ')) ev = line.slice(7)
            else if (line.startsWith('data: ')) data = line.slice(6)
          }
          let d = {}; try { d = JSON.parse(data) } catch { }
          if (ev === 'meta') {
            cur = d.conv_id; setConvId(cur)
            if (cur) history.replaceState(null, '', '?conv=' + cur)   // A：新会话也写进 URL
            if (d.title) setTitle(d.title)
          }
          else if (ev === 'reasoning') {
            if (!asst._r0) asst._r0 = Date.now()
            asst.reasoning += d.text || ''
            asst.rsecs = (Date.now() - asst._r0) / 1000
            push()
          }
          else if (ev === 'delta') { asst.content += d.text || ''; push() }
          else if (ev === 'tool_start') {
            setPendingTools(p => [...p, { id: Math.random().toString(36).slice(2), name: d.name, args: d.args, t0: Date.now() }])
          }
          else if (ev === 'tool') {
            // 当前轮定稿（保留正文/思考），工具结果单独成气泡，再开新一轮，避免"只看到一块"
            setPendingTools(p => { const key = d.name + '|' + (d.args || ''); const k = p.findIndex(x => x.name + '|' + (x.args || '') === key); return k >= 0 ? p.filter((_, idx) => idx !== k) : p.slice(1) })
            const doneAsst = { ...asst }
            asst = { role: 'assistant', content: '', reasoning: '', at: now() }
            setMsgs(m => [...m.filter(x => !x.__live), doneAsst,
              { role: 'tool', toolName: d.name, toolArgs: d.args, content: d.text || '', ms: d.ms || 0, at: now(), __liveTool: true }])
            stream.clear()
          }
          else if (ev === 'error') { asst.content += '\n✗ ' + (d.error || ''); push() }
          else if (ev === 'interject') { setStatus('模型已收到插话：' + (d.text || '').slice(0, 30)) }
          else if (ev === 'done') { setSecs(d.secs); setStatus(`完成 · ${(d.secs || 0).toFixed(1)}s`) }
            else if (ev === 'usage') { if (d.tps > 0) setTps(d.tps) }
        }
      }
      // 收流即定稿：先停掉节流里还没落地的 push，否则它会在 clear 之后把旧内容塞回缓冲
      //（表现：正文已落库，底下却多一个"生成中…"的幽灵气泡）
      ended = true
      if (pushTimer) { clearTimeout(pushTimer); pushTimer = null }
      stream.clear()
      await refresh(); if (cur) await openConv(cur)
    } catch (e) {
        // 不是用户主动停：若后端仍在跑，说明只是流断了 → 重新接上（而不是假装结束）
        if (!aborted && cur) {
          const rr = await api('/api/running').catch(() => [])
          if ((rr || []).includes(String(cur))) {
            setStatus('连接断开，正在重连…')
            await attachStream(cur)
            return
          }
        }
      if (e.name === 'AbortError') {
        setStatus('已停止')
        if (cur) { const cc = await api('/api/conv/' + cur).catch(() => null); if (cc) { setMsgs(normalize(cc.messages || [])); scrollToBottom() } }
      }
      else setStatus('✗ ' + e.message + '（可重试）')
    } finally {
      abortRef.current = null
      ended = true
      if (pushTimer) { clearTimeout(pushTimer); pushTimer = null }
      stream.clear()
      setBusy(false); refresh(); loadCtx(cur)   // 中断/报错也刷新列表，避免会话"看不见"
    }
  }

  const act = async (kind, id, e) => {
    e?.stopPropagation()
    if (kind === 'archive') await fetch('/api/conv/' + id, { method: 'DELETE' })
    if (kind === 'restore') await fetch('/api/conv/' + id + '/restore', { method: 'POST' })
    refresh()
  }

  const rawList = tab === 'archive' ? archived : convs
  // 子会话显示在父会话之后（缩进 + ↳#N）；父不在本列表时按顶层显示
  const byId = new Map(rawList.map(c => [c.ID, c]))
  const kidsOf = new Map()
  for (const c of rawList) {
    if (!c.ParentID) continue
    const k = String(c.ParentID)
    if (!kidsOf.has(k)) kidsOf.set(k, [])
    kidsOf.get(k).push(c)
  }
  const list = []
  for (const c of rawList) {
    if (c.ParentID && byId.has(String(c.ParentID))) continue
    list.push(c)
    for (const k of (kidsOf.get(c.ID) || []).sort((a, b) => a.Seq - b.Seq)) list.push(k)
  }
  const cur = profiles.find(p => p.name === profile)
  // 轮次进度：本轮工具轮次 / 本会话落库的最大 seq（也即已记录的轮次）
  const dirtyActions = anyDirty ? (
    <span className="card-actions title-actions">

        <button className="btn" onClick={() => {
          if (editorDirty) {
            if (editField) setEditP({ ...editP, [editField]: editOriginal })
            else setToolEdit({ ...toolEdit, description: editOriginal })
          } else if (formDirty) {
            setEditP(JSON.parse(editSnap || '{}'))
          } else {
            setSettings(JSON.parse(setSnap || '{}'))
          }
        }}>取消</button>
        <button className="btn" onClick={async () => {
          if (editorDirty) {
            if (toolEdit) {
              await fetch('/api/tools/' + toolEdit.name, { method: 'PUT', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ description: toolEdit.description }) })
              setEditOriginal(toolEdit.description || ''); refresh()
            } else { setEditOriginal(editP?.[editField] || '') }
            return
          }
          if (formDirty) {
            if (!editP.name || !editP.base_url || !editP.model) { alert('名称 / 接口地址 / 模型 必填'); return }
            const { _new, ...body } = editP
            await fetch('/api/profiles', { method: 'POST', headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ ...body, tools: (body.tools || '').split(',').map(x => x.trim()).filter(Boolean), keep_messages: body.keep_messages || 16 }) })
            setEditSnap(snapOf(editP)); refresh(); return
          }
          await fetch('/api/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(settings) })
          setSetSnap(JSON.stringify(settings))
        }}>保存</button>
      
    </span>
  ) : null

  // 分组/统计是纯函数（conversation/messageGroups.js）；这里一次算好，作为 props 传给展示组件
  const isGhostMsg = isGhostM
  const seqOf = (m) => m.seq || liveNo.get(m) || 0

  // 每个工具行的组信息：组起点 / 首尾 / 组统计 / 组前思考
  // 同一份 msgs 下对象引用不变 ⇒ ToolGroup 的 memo 才能命中
  const toolMeta = useMemo(() => {
    const out = new Map()
    const byStart = new Map()
    for (let i = 0; i < msgs.length; i++) {
      if (msgs[i].role !== 'tool') continue
      const start = runStart(msgs, i)
      let stats = byStart.get(start)
      if (!stats) { stats = runStats(msgs, start); byStart.set(start, stats) }
      out.set(i, { start, first: start === i, last: neighborRole(msgs, i + 1, 1) !== 'tool', stats, think: thinkBefore(msgs, i) })
    }
    return out
  }, [msgs])

  // 稳定回调（memo 的依赖，不能每次渲染都新建）
  const toggleRun = useCallback((start) => {
    setOpenRuns(prev => { const n = new Set(prev); n.has(start) ? n.delete(start) : n.add(start); return n })
  }, [])
  // 收起思考后回到这条消息的开头（比"思考过程"那一行再高一点）
  const collapseMsg = useCallback((box) => {
    requestAnimationFrame(() => {
      box.scrollIntoView({ block: 'start' })
      const th = threadRef.current
      if (th) th.scrollTop -= 12
    })
  }, [])

  return (
    <div className="app">
      <div className="topbar">
        <div className="mobrail">
          <span className={'mob-dot' + (busy ? ' busy' : '')} title={busy ? '运行中' : '空闲'} />
          <span className="mob-fig" title="本轮 / 上限">{String(roundNow).padStart(2,'0')}/{roundMax ? String(roundMax).padStart(2,'0') : '∞'}</span>
          {tps > 0 ? <span className="mob-tps"><b>{Math.round(tps)}</b> tok/s</span> : null}
          {ctx ? (
            <span className={'mob-ctx' + ((((ctx.est_tokens || 0) / (ctx.limit_tokens || 105000)) * 100) >= (ctx.compact_pct || 80) ? ' hot' : '')}
              title={Math.round((ctx.est_tokens || 0) / 1000) + ' / ' + Math.round((ctx.limit_tokens || 105000) / 1000) + 'k tokens'}>
              {Math.round(((ctx.est_tokens || 0) / (ctx.limit_tokens || 105000)) * 100)}%
            </span>
          ) : null}
          <span className="mob-sp" />
          <button className="mob-btn" onClick={() => openConv(null)} aria-label="新会话" title="新会话">
            <svg className="plus" viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 4v16M4 12h16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square"/>
            </svg>
          </button>
          <button className="mob-btn" onClick={() => setShowSet(true)} aria-label="设置" title="设置"><i /><i /><i /></button>
        </div>
        <div className="topline">
          <i style={{ width: roundMax ? Math.min(100, roundNow / roundMax * 100) + '%' : '0%' }} />
        </div>
      </div>
      {showSet ? <div className="set-scrim" onClick={() => closeOne()} /> : null}

      {sideOpen ? <div className="drawer-scrim" onClick={() => setSideOpen(false)} /> : null}
      <div className="work" style={{ '--sidew': (sideOpen ? sideW : 0) + 'px' }}>
        <aside className={'drawer' + (sideOpen ? '' : ' hide')}
          style={{ width: sideW, flexBasis: sideW }}
          >
          <span className="edge" onMouseDown={startDrag} aria-hidden="true" />
          <div className="ef-figs drawer-figs">
            <a className={tab === 'live' ? 'on' : ''} onClick={() => setTab('live')}
               title="只看会话"><FigCell value={convs.length} /><span>会话</span></a>
            <a className={tab === 'archive' ? 'on' : ''} onClick={() => setTab('archive')}
               title="只看归档"><FigCell value={archived.length} /><span>归档</span></a>
            <a title="当前会话最后一条消息的落库序号"><FigCell value={msgs.length ? (msgs[msgs.length - 1].seq || 0) : 0} /><span>本轮</span></a>
          </div>
          <div className="list" style={{ padding: '0 .5rem' }}>
            {list.length === 0 ? (
              <div style={{ padding: '.9rem .4rem', color: 'var(--rail-dim)', fontSize: '.78rem' }}>没有记录</div>
            ) : (
              <ol className="ef-records dark">
                {list.map((c, i) => (
                  <li key={c.ID}>
                    <a className={'ef-record' + (c.ID === convId ? ' on' : '') + (c.ParentID ? ' child' : '')}
                      onClick={() => openConv(c.ID)} title={c.ParentID ? ('子会话 ↳#' + c.Seq + '（父 #' + c.ParentID + '）') : ''}>
                      <span className="ef-tag" title={c.ParentID ? ('子会话 #' + c.Seq + '（父 #' + c.ParentID + '）') : c.ID}>
                        {c.ParentID ? '#' + c.Seq : 'No.' + String(c.ID).padStart(4, '0')}</span>
                      {c.ParentID
                        ? <span className="ef-field child-no" title="会话编号">No.{String(c.ID).padStart(4, '0')}</span>
                        : <time className="ef-field" title="最后活跃">{fmtActive(c.UpdatedAt)}</time>}
                      <span className="name ef-read">{c.Title || '(无标题)'}</span>
                        <span className="ef-field count">{c.N}</span>
                        <button className="act" title={tab === 'archive' ? '恢复' : '归档'}
                          onClick={e => { e.stopPropagation(); act(tab === 'archive' ? 'restore' : 'archive', c.ID, e) }}>
                          <svg viewBox="0 0 24 24" aria-hidden="true">
                            {/* 归档 = 箱子 + 向下箭头；恢复 = 箱子 + 向上箭头（不是垃圾桶 ✗） */}
                            <path d="M3 4h18v4H3z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="miter" />
                            <path d="M5 8v12h14V8" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="miter" />
                            <path d={tab === 'archive' ? 'M12 16v-6m-3 3 3-3 3 3' : 'M12 10v6m-3-3 3 3 3-3'}
                              fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="square" strokeLinejoin="miter" />
                          </svg>
                        </button>
                    </a>
                  </li>
                ))}
              </ol>
            )}
          </div>
          <div className="drawer-spacer">
            <button className="new-conv" onClick={() => openConv(null)} title="新会话" aria-label="新会话">
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="M12 4v16M4 12h16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square" />
              </svg>
            </button>
          </div>
          <div className="drawer-foot">
            <button className="foot-btn wide-hide" onClick={() => setShowSet(true)} title="设置">设置</button>
            <span className="foot-mark">WORKSPACE / AGENT</span>
          </div>
        </aside>
        <button className={'side-toggle' + (sideOpen ? '' : ' collapsed')}
          onPointerDown={e => { e.preventDefault(); setSideOpen(v => !v) }}
          title={sideOpen ? '收起会话列表' : '展开会话列表'}
          aria-label={sideOpen ? '折叠侧栏' : '展开侧栏'}
          aria-expanded={sideOpen}>
          <svg className="st-icon" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M14 6 8 12l6 6" fill="none" stroke="currentColor" strokeWidth="1.8"
              strokeLinecap="square" strokeLinejoin="miter" />
          </svg>
        </button>

        <main className="stage" ref={stageRef} style={{ '--colw': colW + 'px' }}
          onMouseMove={onStageMove} onMouseLeave={onStageLeave} onMouseDown={onStageDown}>
        {(() => {
          const me = convs.find(c => c.ID === convId)
          if (!me || !me.ParentID) return null
          return (
            <div className="childbar">
              <span className="childbar-tag">#{me.Seq}</span>
              <span className="childbar-txt">子会话 · 父 <a onClick={() => openConv(String(me.ParentID))}>No.{String(me.ParentID).padStart(4, '0')}</a></span>
            </div>
          )
        })()}
          <i className="guideline" aria-hidden="true" />
          <div className="thread" ref={threadRef} onScroll={onThreadScroll}
            onWheel={e => { if (e.deltaY < 0) stickRef.current = false }}>
            {msgs.length === 0 ? (
              <div className="col empty">
                <div className="eyebrow">LOCAL AGENT CONSOLE</div>
                <h1>选一个 profile，说一句话<br /><em>剩下的交给它：调工具、读屏幕、查知识库</em></h1>
                <p className="lead">过程会留在左侧记录里；工具输出、思考链、截图都会附在回复下面。</p>
                <div className="panel ef-panel cut-tl">
                  <div className="hd"><span>START HERE</span><span>03 STEPS</span></div>
                  <div className="step">
                    <span className="n">01</span>
                    <span className="t"><b>新建会话</b><span>从空白开始，或点左侧任一历史记录继续</span></span>
                    <button onClick={() => openConv(null)}>新建</button>
                  </div>
                  <div className="step">
                    <span className="n">02</span>
                    <span className="t"><b>选 profile</b><span>180-think 规划 · 180-low 快速 · 9-exec 只跑命令</span></span>
                    <button onClick={() => document.querySelector('.composer select')?.focus()}>选择</button>
                  </div>
                  <div className="step">
                    <span className="n">03</span>
                    <span className="t"><b>写一句话</b><span>Ctrl+Enter 发送；要中断就刷新页面</span></span>
                    <button onClick={() => document.querySelector('.composer textarea')?.focus()}>去输入</button>
                  </div>
                </div>
                <div className="tips">
                  <span>会话 <code id="n1">{convs.length}</code></span>
                  <span>归档 <code>{archived.length}</code></span>
                  <span>当前 <code>{profile}</code></span>
                </div>
              </div>
            ) : (
              <div className="col">
                {msgs.map((m, i) => {
                  if (isGhostMsg(m)) return null   // 工具轮的 assistant 只有思考和 tool_calls：不占 Agent 卡
                  const seq = seqOf(m)
                  if (m.role === 'tool') {
                    const gmeta = toolMeta.get(i)
                    if (!gmeta) return null
                    return <ToolGroup key={i} m={m} seq={seq} meta={gmeta}
                              open={openRuns.has(gmeta.start)} onToggle={toggleRun} />
                  }
                  if (isSysNote(m)) return <SysNote key={i} m={m} seq={seq} />
                  return <MessageCard key={i} m={m} seq={seq} vidx={vidxOf.get(m)} onCollapse={collapseMsg} />
                })}
                <LiveBubble busy={busy} />
                {pendingTools.map(p => (
                  <div key={p.id} className="msg ef-panel cut-tl tool pending-tool">
                    <div>
                      <div className="mhead">
                        <span className="av t">T</span>
                        <b>{p.name}</b>
                        <span className="ef-field">执行中…</span>
                        <Elapsed t0={p.t0} />
                      </div>
                      <div className="tool-args">{(p.args || '').slice(0, 160)}</div>
                    </div>
                  </div>
                ))}
                <div ref={endRef} />
              </div>
            )}
          </div>
          <div className="status" onWheel={onComposerWheel}>
              {busy
                ? (stage === 'compacting' ? '压缩中…（上下文超阈值，正在写摘要）' : (stage === 'generating' ? '生成中…　' + quip : (quip || '正在处理…')))
                : status}
            </div>
          <div className="composer" onWheel={onComposerWheel}>
            <div className="row1">
              <span className="eb" style={{ display: 'inline', letterSpacing: '.14em' }}>PROFILE</span>
              <label className="pick">
                <select value={profile} onChange={e => setProfile(e.target.value)}>
                  {(profiles.length ? profiles : [{ name: profile, model: '' }]).map(p => (
                    <option key={p.name} value={p.name}>{p.name}</option>
                  ))}
                </select>
                <span className="caret">▾</span>
              </label>
              <span className="model" style={{ marginLeft: 'auto' }} title={cur ? cur.model : ''}>{cur && cur.model ? cur.model : (profile === '9-exec' ? 'Qwen3.5-9B-int4-ov' : 'qwen3.8-flash-next-iq3_xxs')}</span>
            </div>
            <i className="resizer" onMouseDown={startResize} title="拖动调整输入框高度" />
            <textarea value={text} style={{ height: inputH + 'px' }} onChange={e => setText(e.target.value)}
              onKeyDown={e => {
                if (e.key !== 'Enter' || e.shiftKey || e.nativeEvent.isComposing) return   // Shift+Enter 换行 ✓
                e.preventDefault()
                if (!busy) send()
                else if (text.trim()) interject()
                else stop()
              }}
              placeholder="输入你的问题…" />
            <div className="row3">
              <span className="hint">Enter 发送 · Shift+Enter 换行　{convId ? '#' + convId.slice(0, 8) : '新会话'}</span>
              {busy ? (
                text.trim()
                  ? <button className="send interject" onClick={interject} aria-label="插话" title="插话" />
                  : <button className="send stop" onClick={stop} aria-label="中断" title="中断" />
              ) : <button className="send" onClick={send} disabled={!text.trim()}
                    aria-label="发送" title={text.trim() ? '发送' : '先输入内容'} />}
            </div>
          </div>
          {showSet ? (
            <aside className={'settings' + ((editField || toolEdit) ? (editP ? ' three' : ' two') : (editP ? ' two' : ''))}>
              <div className={'set-cols' + ((editField || toolEdit) ? (editP ? ' three' : ' two') : ((editP || toolCfg) ? ' two' : ''))}
                onClick={e => { if (e.target === e.currentTarget) closeOne() }}>
                {(editField || toolEdit) ? (
                  <div className={"card col-editor" + ((editField || toolEdit) ? " is-top" : "")}>
                    <div className="card-hd"><button className="icon-x back card-back narrow-only" onClick={() => closeOne()} aria-label="关闭" title="关闭"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="square"/></svg></button><span className="ef-label"><span className="ef-label-t">{toolEdit ? ('tool · ' + toolEdit.name) : 'system'}</span></span>
                        <span style={{ flex: 1 }} />
                        {narrow ? dirtyActions : null}</div>
                    <textarea ref={el => { if (el) autoGrow(el) }} className="set-area" value={toolEdit ? (toolEdit.description || '') : (editP[editField] || '')}
                      onInput={e => autoGrow(e.target)}
                      placeholder={toolEdit ? '工具说明（模型会看到这段文字）…' : '这里写完整的 system 提示…'}
                      onChange={e => {
                        if (toolEdit) setToolEdit({ ...toolEdit, description: e.target.value })
                        else setEditP({ ...editP, [editField]: e.target.value })
                      }} />
                    <div className="set-col-foot"><span className="set-meta">
                      {(toolEdit ? (toolEdit.description || '') : (editP[editField] || '')).length} 字
                    </span></div>
                  </div>
                ) : null}

                {(toolCfg || toolForm) ? (
                <div className={"card col-tool" + ((toolCfg || toolForm) && !toolEdit && !editField ? " is-top" : "")}>
                  <div className="card-hd">
                    <button className="icon-x back card-back narrow-only" onClick={() => closeOne()} aria-label="关闭" title="关闭"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="square"/></svg></button>
                    <span className="ef-label"><span className="ef-label-t">{toolForm ? (toolForm.isNew ? 'new plugin' : 'tool · ' + toolForm.name) : ('tool · ' + toolCfg.name)}</span></span>
                    <span style={{ flex: 1 }} />
                    {toolForm ? (
                      <span className="card-actions title-actions">
                        <button className="btn" onClick={() => { setToolForm(null); setToolCfg(null) }}>取消</button>
                        <button className="btn" onClick={async () => {
                          if (!toolForm.name || !toolForm.command.trim()) { alert('名字与命令必填'); return }
                          let params = null
                          try { params = toolForm.paramsText.trim() ? JSON.parse(toolForm.paramsText) : null } catch { alert('参数不是合法 JSON'); return }
                          const body = {
                            name: toolForm.name.trim(), description: toolForm.description || '',
                            command: toolForm.command.split('\n').map(x => x.trim()).filter(Boolean),
                            timeoutMs: Number(toolForm.timeoutMs) || 20000, persistent: !!toolForm.persistent,
                            params, required: (toolForm.requiredText || '').split(',').map(x => x.trim()).filter(Boolean)
                          }
                          const r = await fetch('/api/plugins', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
                          const j = await r.json().catch(() => ({}))
                          if (!r.ok) { alert(j.error || '保存失败'); return }
                          setToolForm(null); setToolCfg(null); refresh(); setToolList((await api('/api/tools')) || [])
                        }}>保存</button>
                      </span>
                    ) : (narrow ? dirtyActions : null)}
                  </div>
                  
                  {toolForm ? (
                    <div className="card-bd">
                      <label className="field"><span>名称（小写字母开头，a-z 0-9 _）</span>
                        <input value={toolForm.name} disabled={!toolForm.isNew}
                          onChange={e => setToolForm({ ...toolForm, name: e.target.value })} /></label>
                      <label className="field"><span>说明（模型看到的描述，也可在三级卡里改）</span>
                        <input value={toolForm.description}
                          onChange={e => setToolForm({ ...toolForm, description: e.target.value })} /></label>
                      <label className="field"><span>命令（每行一个参数，直接 exec 不走 shell）</span>
                        <textarea className="plugin-cmd" value={toolForm.command}
                          placeholder={'~/Workspace/agentd/scripts/tools/kb_get\n{{id}}'}
                          onChange={e => setToolForm({ ...toolForm, command: e.target.value })} /></label>
                      <label className="field"><span>超时（毫秒）</span>
                        <input type="number" min="1000" value={toolForm.timeoutMs}
                          onChange={e => setToolForm({ ...toolForm, timeoutMs: e.target.value })} /></label>
                      <div className="item static">
                        <span className="item-title">常驻进程（JSON-RPC over stdio）</span>
                        <button type="button" role="switch" aria-checked={!!toolForm.persistent}
                          className={'tg' + (toolForm.persistent ? ' on' : '')}
                          onClick={() => setToolForm({ ...toolForm, persistent: !toolForm.persistent })}><b /></button>
                      </div>
                      <label className="field"><span>参数（JSON Schema properties）</span>
                        <textarea className="plugin-json" value={toolForm.paramsText}
                          onChange={e => setToolForm({ ...toolForm, paramsText: e.target.value })} /></label>
                      <label className="field"><span>必填参数（逗号分隔）</span>
                        <input value={toolForm.requiredText}
                          onChange={e => setToolForm({ ...toolForm, requiredText: e.target.value })} /></label>
                      {!toolForm.isNew ? (
                        <div className="card-foot del-bottom-wrap" style={{ display: 'flex' }}>
                          <button className="btn danger del-bottom" onClick={async () => {
                            if (!confirm('删除插件 ' + toolForm.name + '？（只删清单，脚本文件保留）')) return
                            const r = await fetch('/api/plugins/' + toolForm.name, { method: 'DELETE' })
                            const j = await r.json().catch(() => ({}))
                            if (!r.ok) { alert(j.error || '删除失败'); return }
                            setToolForm(null); setToolCfg(null); refresh(); setToolList((await api('/api/tools')) || [])
                          }}>删除插件</button>
                        </div>
                      ) : null}
                    </div>
                  ) : null}

                  {!toolForm ? (
                    <div className="card-bd">
                    <div className="item" onClick={() => { setEditOriginal(toolCfg.description || ''); setToolEdit({ name: toolCfg.name, description: toolCfg.description }); setEditField(null) }}>
                      <span className="item-title">说明</span>
                      <span className="item-desc">{toolCfg.description || '（空）'}</span>
                    </div>
                    <div className="set-section">来源</div>
                    <div className="item static">
                      <span className="item-title">{toolCfg.source === 'plugin' ? '外置插件' : '内置工具'}</span>
                      <span className="item-desc">{toolCfg.source === 'plugin' ? '清单 + 脚本，改完即生效' : 'Go 内置，改需重编译'}</span>
                    </div>
                    {toolCfg.source === 'plugin' ? (
                      <>
                        <div className="field"><span>清单</span>
                          <div className="tool-meta">{toolCfg.path}</div></div>
                        <div className="field"><span>超时</span>
                          <div className="tool-meta">{toolCfg.timeout_ms ? toolCfg.timeout_ms + ' ms' : '默认（20s）'}
                            {toolCfg.persistent ? ' · 常驻进程（JSON-RPC over stdio）' : ' · 一次性进程'}</div></div>
                      </>
                    ) : null}

                    <div className="set-section">启用情况</div>
                    <div className="item static">
                      <span className="item-title">由 profile 决定</span>
                    </div>
                    <div className="set-meta">
                      {(() => {
                        const on = (profiles || []).filter(pr => (pr.tools || []).includes(toolCfg.name)).map(pr => pr.name)
                        return on.length ? '在 ' + on.join('、') + ' 中启用' : '当前没有任何 profile 启用它'
                      })()}
                    </div>
                    <div className="set-meta">要改可用性：打开对应模型的「工具」勾选</div>
                  </div>
                  ) : null}
                </div>
                ) : null}
                {editP ? (
                  <div className={"card col-form" + (!(editField || toolEdit) && editP ? " is-top" : "")}>
                    <div className="card-hd" style={{ display: 'flex', alignItems: 'center', gap: '.6rem' }}>
                      <button className="icon-x back card-back narrow-only" onClick={() => closeOne()} aria-label="关闭" title="关闭"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="square"/></svg></button>
                      <span className="ef-label"><span className="ef-label-t">{editP._new ? '新建模型' : ('模型详情 · ' + editP.name)}</span></span>
                      <span style={{ flex: 1 }} />
                      {narrow ? dirtyActions : null}
                      {!editP._new ? (
                        <button className="btn danger del-top" onClick={async () => {
                          if (!confirm('删除 ' + editP.name + '？')) return
                          await fetch('/api/profiles/' + editP.name, { method: 'DELETE' })
                          setEditP(null); setEditField(null); refresh()
                        }}>删除</button>
                      ) : null}
                    </div>
                    <div className="card-bd">
                      {[['name', '名称'], ['base_url', '接口地址 (url+/v1)'],
                        ['api_key', 'API Key']].map(([k, label]) => (                        <label className="field" key={k}><span>{label}</span>
                          <input value={editP[k] || ''} type={k === 'api_key' ? 'password' : 'text'}
                            onChange={e => setEditP({ ...editP, [k]: e.target.value })} /></label>
                      ))}
                      <label className="field"><span className="field-label">模型</span>
                        {models.loading ? <span className="item-desc">正在匹配模型…</span>
                         : models.err ? <span className="item-desc">检查接口地址或 API Key：{models.err}</span>
                         : models.list.length ? (
                          <select value={editP.model || ''} onChange={e => setEditP({ ...editP, model: e.target.value })}>
                            {models.list.map(m => <option key={m} value={m}>{m}</option>)}
                          </select>
                         ) : <input value={editP.model || ''} onChange={e => setEditP({ ...editP, model: e.target.value })} placeholder="没取到模型列表，可手填" />}
                      </label>
                      <label className="field"><span>思考强度</span>
                        <select value={editP.reasoning_effort || 'none'} onChange={e => setEditP({ ...editP, reasoning_effort: e.target.value })}>
                          {['none', 'low', 'medium', 'high', 'max'].map(x => <option key={x} value={x}>{x}</option>)}
                        </select></label>
                      <label className="field"><span>服务商（云端 OpenAI 兼容填 openai：不发本地扩展字段）</span>
                        <select value={editP.provider || ''} onChange={e => setEditP({ ...editP, provider: e.target.value })}>
                          <option value="">自动 / 本地（Strata、llama.cpp）</option>
                          <option value="openai">openai（云端兼容接口）</option>
                        </select></label>
                      <label className="field"><span>上下文窗口（tokens，留空=自动读上游）</span>
                        <input type="number" min="0" max="2000000" placeholder="自动（如 131072）"
                          value={editP.context_window || ''}
                          onChange={e => setEditP({ ...editP, context_window: Number(e.target.value) || 0 })} />
                      </label>
                      <label className="field"><span>输入上限（tokens，留空=105000）</span>
                        <input type="number" min="0" max="1000000" placeholder="105000"
                          value={editP.max_prompt_tokens || ''}
                          onChange={e => setEditP({ ...editP, max_prompt_tokens: Number(e.target.value) || 0 })} />
                      </label>
                      <label className="field"><span>压缩阈值（%，留空=80）</span>
                        <input type="number" min="1" max="99" placeholder="80"
                          value={editP.compact_pct || ''}
                          onChange={e => setEditP({ ...editP, compact_pct: Number(e.target.value) || 0 })} />
                      </label>
                      <div className="field"><span>工具</span>
                        <div className="choices">
                          {toolList.map(t => {
                            const on = (editP.tools || '').split(',').map(x => x.trim()).filter(Boolean).includes(t.name)
                            return (
                              <label key={t.name} title={t.description || ''} className={'choice' + (on ? ' on' : '')}>
                                <input type="checkbox" checked={on} onChange={e => {
                                  const cur = (editP.tools || '').split(',').map(x => x.trim()).filter(Boolean)
                                  const next = e.target.checked ? [...cur, t.name] : cur.filter(x => x !== t.name)
                                  setEditP({ ...editP, tools: next.join(',') })
                                }} />
                                <span>{t.name}</span>
                              </label>
                            )
                          })}
                        </div>
                      </div>
                      <div className="item" onClick={() => { setEditOriginal(editP.system || ''); setEditField('system'); setToolEdit(null) }}>
                        <span className="item-title">system</span>
                        <span className="item-desc">{editP.system || '（空）'}</span>
                      </div>
                      {!editP._new ? (
                        <div className="card-foot del-bottom-wrap">
                          <button className="btn danger del-bottom" onClick={async () => {
                            if (!confirm('删除 ' + editP.name + '？')) return
                            await fetch('/api/profiles/' + editP.name, { method: 'DELETE' })
                            setEditP(null); setEditField(null); refresh()
                          }}>删除配置</button>
                        </div>
                      ) : null}
                    </div>
                  </div>
                ) : null}

                <div className={"card col-list" + (!(editField || toolEdit) && !editP ? " is-top" : "")}>
                  <div className="card-hd list-hd">
                    <button className="icon-x back card-back narrow-only" onClick={() => closeOne()} aria-label="退出设置" title="退出"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="square"/></svg></button>
                    <span className="ef-label"><span className="ef-label-t">SETTINGS / PROFILES</span></span>
                      <span style={{ flex: 1 }} />
                      {!narrow ? dirtyActions : null}
                  </div>
                  <div className="card-bd">
                    <div className="set-section">常规配置</div>
                    <div className="set-card">
                      <div className="item static">
                        <span className="item-title">工具调用轮次上限</span>
                        <button type="button" role="switch" aria-checked={!!settings.tool_rounds_enabled}
                          className={'tg' + (settings.tool_rounds_enabled ? ' on' : '')}
                          onClick={() => setSettings({ ...settings, tool_rounds_enabled: !settings.tool_rounds_enabled })}><b /></button>
                      </div>
                      <div className="set-meta">开启该项以避免工具循环调用</div>
                      {settings.tool_rounds_enabled ? (
                        <label className="field" style={{ marginTop: '.5rem' }}><span>轮次上限</span>
                          <input type="number" min="1" max="1000" value={settings.tool_rounds || 8}
                            onChange={e => setSettings({ ...settings, tool_rounds: Number(e.target.value) || 8 })} /></label>
                      ) : null}
                    </div>

                    <div className="set-card">
                      <div className="item static">
                        <span className="item-title">自动归档</span>
                        <button type="button" role="switch" aria-checked={!!settings.auto_archive_enabled}
                          className={'tg' + (settings.auto_archive_enabled ? ' on' : '')}
                          onClick={() => setSettings({ ...settings, auto_archive_enabled: !settings.auto_archive_enabled })}><b /></button>
                      </div>
                      <div className="set-meta">超过设定天数未活跃的会话自动归入归档</div>
                      {settings.auto_archive_enabled ? (
                        <label className="field" style={{ marginTop: '.5rem' }}><span>未活跃天数</span>
                          <input type="number" min="1" max="3650" value={settings.auto_archive_days || 7}
                            onChange={e => setSettings({ ...settings, auto_archive_days: Number(e.target.value) || 7 })} /></label>
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
                        <button type="button" role="switch" aria-checked={!!settings.shots_auto_clean}
                          className={'tg' + (settings.shots_auto_clean ? ' on' : '')}
                          onClick={() => setSettings({ ...settings, shots_auto_clean: !settings.shots_auto_clean })}><b /></button>
                      </div>
                      <div className="set-meta">超过保留天数的截图文件自动删除（数据库里的消息不受影响）</div>
                      {settings.shots_auto_clean ? (
                        <label className="field" style={{ marginTop: '.5rem' }}><span>保留天数</span>
                          <input type="number" min="1" max="3650" value={settings.shots_keep_days || 7}
                            onChange={e => setSettings({ ...settings, shots_keep_days: Number(e.target.value) || 7 })} /></label>
                      ) : null}
                      <div className="item static" style={{ marginTop: '.4rem' }}>
                        <span className="item-desc" style={{ margin: 0 }}>{shotInfo || '—'}</span>
                        <button className="btn" onClick={async () => {
                          const r = await fetch('/api/shots/cleanup', { method: 'POST', headers: { 'Content-Type': 'application/json' },
                            body: JSON.stringify({ days: settings.shots_keep_days || 7 }) }).then(x => x.json())
                          setShotInfo(`已清理 ${r.removed} 张，释放 ${r.freed_kb} KB`)
                          loadShotStats()
                        }}>立即清理</button>
                      </div>
                    </div>

                    <div className="set-section">模型配置</div>
                    <div className="set-card">
                      {profiles.map(pr => (
                        <div className={'item col' + (editP && editP.name === pr.name ? ' on' : '')} key={pr.name}
                          onClick={() => { const v = { ...pr, tools: (pr.tools || []).join(',') }; setEditP(v); setEditSnap(snapOf(v)); setEditField(null); setToolEdit(null); setToolCfg(null) }}>
                          <b className="item-title">{pr.name}</b>
                          <span className="item-desc">{pr.model || '（未设置模型）'}</span>
                        </div>
                      ))}
                      <div className="item item-new" onClick={() => {
                        const v = { _new: true, name: '', base_url: 'http://127.0.0.1:18320/180/v1', api_key: '',
                          model: 'qwen3.8-flash-next-iq3_xxs', reasoning_effort: 'high', tools: '', system: '' }
                        setEditP(v); setEditSnap(''); setEditField(null); setToolEdit(null)
                      }}>＋ 新建 profile</div>
                    </div>

                    <div className="set-section">工具配置</div>
                    <div className="set-card">
                      {[...toolList].sort((a, b) => {
                        const g = (x) => (x.source === 'plugin' ? 1 : 0)   // 内置 0，外置 1
                        const d = g(a) - g(b)
                        return d !== 0 ? d : a.name.localeCompare(b.name)
                      }).map(t => (
                        <div className="item" key={t.name} title={t.path || '内置工具（在程序内指定）'}
                          onClick={() => {
                            setEditP(null); setToolEdit(null); setEditField(null)
                            setToolCfg({ ...t, enabled: t.enabled !== false, subtools: t.subtools || [] })
                            setToolForm(t.source === 'plugin' ? {
                              isNew: false, name: t.name, description: t.description || '',
                              command: (t.command || []).join('\n'), timeoutMs: t.timeout_ms || 20000,
                              persistent: !!t.persistent, paramsText: JSON.stringify(t.params || {}, null, 2),
                              requiredText: (t.required || []).join(',')
                            } : null)
                          }}>
                          <b className="item-title">{t.name}
                            {t.source !== 'plugin' ? <span className="src-tag builtin">内置</span> : null}
                          </b>
                          <span className="item-desc">{t.description}</span>
                        </div>
                      ))}
                    <div className="item item-new" onClick={() => {
                      setEditP(null); setToolEdit(null); setEditField(null); setToolCfg(null)
                      setToolForm({ isNew: true, name: '', description: '', command: '', timeoutMs: 20000,
                        persistent: false, paramsText: '{\n  "query": { "type": "string" }\n}', requiredText: 'query' })
                    }}>＋ 新建插件</div>

                      <div className="set-section">任务{taskActive ? '（' + taskActive + ' 个在跑）' : ''}</div>
                      <div className="set-card">
                        {tasks.length === 0 ? (
                          <div className="set-meta">还没有任务{recipes.length ? '（可用配方：' + recipes.join('、') + '）' : '（未注册配方）'}</div>
                        ) : tasks.map(t => (
                          <div className="item col" key={t.id} onClick={async () => {
                            const d = await api('/api/tasks/' + t.id).catch(() => null)
                            setTaskDetail(prev => (prev && prev.task && prev.task.id === t.id) ? null : d)
                          }}>
                            <b className="item-title">#{t.id} {t.kind}
                              <span className={'src-tag ' + (t.status === 'done' ? 'builtin' : 'plugin')}>{t.status}</span>
                            </b>
                            <span className="item-desc">{t.child_conv ? '子会话 ↳#' + t.child_conv + ' · ' : ''}{t.error || fmtTime(t.created_at)}</span>
                          </div>
                        ))}
                        {taskDetail ? (
                          <div className="item static" style={{ display: 'block' }}>
                            {(taskDetail.steps || []).map(s => (
                              <div key={s.seq} className="task-step">
                                <span className={'ts-dot ' + s.status} />
                                <span className="ts-cmd">{s.cmd || s.name}</span>
                                <span className="ts-meta">{s.exit_code ? 'exit ' + s.exit_code : ''} {s.ms}ms</span>
                                {s.output ? <pre className="ts-out">{s.output.slice(0, 300)}</pre> : null}
                              </div>
                            ))}
                            {taskDetail.task && taskDetail.task.child_conv ? (
                              <button className="btn" style={{ marginRight: '.4rem' }}
                                onClick={() => { openConv(String(taskDetail.task.child_conv)); setShowSet(false) }}>
                                打开子会话 No.{String(taskDetail.task.child_conv).padStart(4, '0')}</button>
                            ) : null}
                            {taskDetail.task && taskDetail.task.status === 'interrupted' ? (
                              <button className="btn" onClick={async () => {
                                await api('/api/tasks/' + taskDetail.task.id + '/restore', { method: 'POST' })
                                setTaskDetail(null); loadTasks()
                              }}>执行收尾</button>
                            ) : null}
                          </div>
                        ) : null}
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </aside>
          ) : null}
        </main>
        {peek.open && peekRows.length ? (
          <div className="peek" style={{ top: peek.top || '50%' }}
            onMouseEnter={() => setPeek(p => ({ ...p, open: true }))}
            onMouseLeave={() => setPeek({ open: false, idx: -1, top: 0 })}
            onWheel={e => {
              e.preventDefault()
              const base = peek.idx >= 0 ? peek.idx : curIdx()
              const next = Math.max(0, Math.min(visible.length - 1, base + (e.deltaY > 0 ? 1 : -1)))
              if (next !== base) {
                setPeek(p => ({ ...p, idx: next }))
                scrollToMsg(next)          // 同时滚动页面 → 分子随之更新 ✓
              }
            }}>
            {peekRows.map(({ i, m }) => (
              <button key={i}
                className={'peek-row' + ((peek.idx < 0 ? curIdx() : peek.idx) === i ? ' on' : '')}
                onClick={() => { scrollToMsg(i); setPeek({ open: false, idx: -1 }) }}
                onMouseEnter={() => setPeek(p => ({ ...p, open: true, idx: i }))}>
                <span className="peek-no">{String(i + 1).padStart(3, '0')}</span>
                <span className="peek-tx">{String(m.content || m.role || '').replace(/\s+/g, ' ').slice(0, 18)}</span>
              </button>
            ))}
          </div>
        ) : null}
        <aside className="rail">
          <span className="vt">AGENT EXECUTE · CREATE</span>          <span className="rail-mid">
            <span className="fig" style={{ cursor: 'pointer' }}
              onMouseEnter={e => { const r = e.currentTarget.getBoundingClientRect(); setPeek({ open: true, idx: curIdx(), top: r.top + r.height / 2 }) }}
              onMouseLeave={() => setPeek({ open: false, idx: -1, top: 0 })}
              onWheel={e => {
                e.preventDefault()
                const base = peek.idx >= 0 ? peek.idx : curIdx()
                const next = Math.max(0, Math.min(visible.length - 1, base + (e.deltaY > 0 ? 1 : -1)))
                if (next !== base) {
                  setPeek(p => ({ ...p, idx: next }))
                  scrollToMsg(next)        // 滚动页面 ✓
                }
              }}>
              {String(roundNow).padStart(2,'0')} / {roundMax ? String(roundMax).padStart(2,'0') : '∞'}
            </span>
          </span>
          <div className="rail-foot">
            <div className="rail-acts">
              <button className="dots" onClick={() => openConv(null)} aria-label="新会话" title="新会话">
                <svg className="plus" viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M12 4v16M4 12h16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square"/>
                </svg>
              </button>
              <button className="dots" onClick={() => setShowSet(true)} aria-label="设置" title="设置">
                <i /><i /><i />
              </button>
            </div>
            <div className="rail-group">
              <span className="rail-status"><span className={'dot' + (busy ? ' busy' : '')} />{busy ? '运行中' : '空闲'}</span>
              {tps > 0 ? (
                <span className="rail-tps"><b>{Math.round(tps)}</b> tok/s</span>
              ) : null}
              {ctx ? (
                <span
                  title={Math.round((ctx.est_tokens || 0) / 1000) + ' / ' + Math.round((ctx.limit_tokens || 105000) / 1000) + 'k tokens（已压缩）'
                    + ' · 原始 ' + Math.round(((ctx.raw || 0) / 1.8) / 1000) + 'k tokens · 窗口 '
                    + Math.round((ctx.window || 131072) / 1000) + 'k · 达 ' + (ctx.compact_pct || 80) + '% 自动压缩'}
                  className={'rail-ctx' + ((((ctx.est_tokens || 0) / (ctx.limit_tokens || 105000)) * 100) >= (ctx.compact_pct || 80) ? ' hot' : '')}>
                  <b>{Math.round(((ctx.est_tokens || 0) / (ctx.limit_tokens || 105000)) * 100)}%</b>
                </span>
              ) : null}
            </div>
          </div>
        </aside>
      </div>
    </div>
  )
}
