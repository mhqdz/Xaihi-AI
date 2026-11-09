// 设置面板的容器：状态机 + 保存/取消/关闭 + 深链（?profile=&field=&tool=&tooledit=&newplugin=&task=）
// 展示组件只拿算好的值，不放业务逻辑在这里之外。
import { useEffect, useRef, useState } from 'react'

const api = (p, opt) => fetch(p, opt).then(r => r.json())

// 快照去掉内部标记 _new，避免"一打开就算脏"✗
export const snapOf = (p) => { if (!p) return ''; const { _new, ...rest } = p; return JSON.stringify(rest) }

// 工具表单里"用户能改的内容"。isNew / source / path / originalName / oldName
// 是元数据，改它们不算用户改动。
const TOOL_FIELDS = ['name', 'category', 'description', 'command', 'timeoutMs', 'persistent', 'paramsText', 'requiredText']
// 自定义请求头在界面上用「Key: Value 每行一条」编辑，存的是对象。
export const headersToText = (h) => Object.entries(h || {})
  .map(([k, v]) => k + ': ' + v).join('\n')
export const textToHeaders = (t) => {
  const o = {}
  for (const line of String(t || '').split('\n')) {
    const i = line.indexOf(':')
    if (i <= 0) continue
    const k = line.slice(0, i).trim(), v = line.slice(i + 1).trim()
    if (k) o[k] = v
  }
  return o
}

export const toolSnap = (f) => {
  if (!f) return ''
  const o = {}
  for (const k of TOOL_FIELDS) o[k] = f[k] === undefined ? '' : String(f[k])
  return JSON.stringify(o)
}

const BLANK_SETTINGS = { tool_rounds_enabled: true, tool_rounds: 8 }
const NEW_PROFILE = {
  _new: true, name: '', endpoint: '', model: '',
  reasoning_effort: 'high', tools: '', system: '',
  session_header: '', headersText: ''
}
const NEW_PLUGIN = {
  isNew: true, name: '', category: '', description: '', command: '', timeoutMs: 20000,
  persistent: false, paramsText: '{\n  "query": { "type": "string" }\n}', requiredText: 'query'
}


// 改动清单里的字段名。后端字段直白，但"上下文窗口"比 context_window 好认。
const FIELD_LABELS = {
  name: '名称', base_url: '接口地址', api_key: 'API Key', model: '模型',
  reasoning_effort: '思考强度', tools: '可用工具', system: 'system 提示',
  keep_messages: '保留消息数', context_window: '上下文窗口', compact_pct: '压缩阈值',
  session_header: '会话头', headersText: '自定义请求头', persona: '人格',
  persistent: '持久化会话', input: '输入模态', effort_levels: '思考档位'
}
const TOOL_LABELS = {
  name: '名称', description: '说明', session_header: '会话头', headers: '请求头', command: '命令', timeoutMs: '超时(ms)',
  persistent: '常驻进程', paramsText: 'JSON Schema', requiredText: '必填参数'
}
// 接口 / 模型两层的字段名。三层结构里这两层以前只有"打开看看"，
// 改了也没地方存 —— 现在它们和配置层一样进同一份改动清单。
const ENDPOINT_LABELS = {
  name: '名称', base_url: '接口地址', api_key: 'API Key', provider: '协议', source: '来源模板'
}
const SETTING_LABELS = {
  tool_rounds_enabled: '工具轮次上限开关', tool_rounds: '工具轮次上限',
  auto_archive_enabled: '自动归档开关', auto_archive_days: '自动归档天数',
  shots_auto_clean: '截图自动清理', shots_keep_days: '截图保留天数'
}
const showVal = (v) => {
  if (Array.isArray(v)) return v.join('、') || '（空）'
  if (v === true) return '开'
  if (v === false) return '关'
  if (v === '' || v == null) return '（空）'
  return String(v)
}
// 只列出真正变了的字段
const diffFields = (base, next, labels) => {
  const out = []
  const keys = new Set([...Object.keys(base || {}), ...Object.keys(next || {})])
  for (const k of keys) {
    if (k === '_new') continue
    const a = showVal(base?.[k]); const b = showVal(next?.[k])
    if (a !== b) out.push({ key: k, label: labels[k] || k, from: a, to: b })
  }
  return out
}

export function useSettings({ refresh }) {
  const [showSet, setShowSet] = useState(() => new URLSearchParams(location.search).has('settings'))
  // 焦点管理：打开时记住是谁触发的、并把焦点送进面板；关闭时还回去。
  // 不做这两步，键盘/读屏用户打开后焦点停在 BODY，关掉后也不知道自己回到哪了。
  const lastFocus = useRef(null)
  const wasOpen = useRef(showSet)
  useEffect(() => {
    if (showSet && !wasOpen.current) {
      lastFocus.current = document.activeElement
      // 双 rAF 而不是 setTimeout(0)：点按钮打开时，浏览器要等这一轮点击的默认聚焦
      // 处理完才轮到我们，只排一个宏任务会被它盖掉（实测焦点停在触发按钮上）。
      requestAnimationFrame(() => requestAnimationFrame(() => {
        // 只取真正可见的：面板里第一个可聚焦元素常常是 narrow-only 的返回按钮，
        // 宽屏下是 display:none —— 对不可见元素调 focus() 什么也不会发生。
        const el = [...document.querySelectorAll(
          '.set-cols button, .set-cols input, .set-cols select, .set-cols textarea, .set-cols [tabindex]')]
          .find(e => e.getClientRects().length > 0)
        if (el) el.focus()
      }))
    } else if (!showSet && wasOpen.current) {
      setTimeout(() => { try { lastFocus.current && lastFocus.current.focus() } catch (e) {} }, 0)
    }
    wasOpen.current = showSet
  }, [showSet])
  const [editP, setEditP] = useState(null)
  const [editField, setEditField] = useState(null)
  const [editOriginal, setEditOriginal] = useState('')
  const [editSnap, setEditSnap] = useState('')
  const [toolList, setToolList] = useState([])
  const [personas, setPersonas] = useState([])  // 可插拔人格（名字 + 预览）
  const [toolForm, setToolForm] = useState(null)  // 插件表单（新建/编辑）
  const [toolEdit, setToolEdit] = useState(null)  // 工具说明的三级编辑
  const [toolIssues, setToolIssues] = useState([])  // 加载失败被跳过的插件及原因
  const [endpoints, setEndpoints] = useState([])     // 接口列表（地址 + 凭据）
  const [sources, setSources] = useState([])         // 来源模板（sources.json）
  const [editE, setEditE] = useState(null)           // 正在编辑的接口（2 级）
  const [editESnap, setEditESnap] = useState('')     // 打开接口卡时的快照
  // 上游探测结果：{state: running|ok|error|waitkey, models, error, url}。
  // 一次只开一张 2 级卡，所以一个对象够了，不用按接口名存。
  const [probe, setProbe] = useState(null)
  // 接口名 → 上一次问上游拿到的模型列表 {models, at, stale, error, loading}
  // 只用于"选模型"和"看一眼有哪些"：能力会写进 profile，读配置时不依赖它。
  const [modelList, setModelList] = useState({})
  const [toolSnapShot, setToolSnapShot] = useState(null)  // 工具表单打开时的快照（对象，用于 diff）
  const [pendingNewPlugin, setPendingNewPlugin] = useState(false)
  const [settings, setSettings] = useState(BLANK_SETTINGS)
  const [setSnap, setSetSnap] = useState(() => JSON.stringify(BLANK_SETTINGS))
  const [tasks, setTasks] = useState([])
  const [recipes, setRecipes] = useState([])
  const [taskActive, setTaskActive] = useState(0)
  const [taskDetail, setTaskDetail] = useState(null)
  const [shotInfo, setShotInfo] = useState('')

  const loadTasks = async () => {
    const r = await api('/api/tasks').catch(() => null)
    if (r && !Array.isArray(r)) { setTasks(r.tasks || []); setRecipes(r.recipes || []); setTaskActive(r.active || 0) }
  }
  const loadShotStats = async () => {
    const r = await api('/api/shots/stats').catch(() => null)
    if (r) setShotInfo(`当前 ${r.count} 张，占用 ${r.size_mb.toFixed(1)} MB`)
  }
  const loadEndpoints = async () => {
    setEndpoints((await api('/api/endpoints').catch(() => [])) || [])
    setSources((await api('/api/sources').catch(() => [])) || [])
  }
  const loadPersonas = async () => {
    setPersonas((await api('/api/personas').catch(() => [])) || [])
  }

  const loadTools = async () => {
    setToolList((await api('/api/tools').catch(() => [])) || [])
    // 哪些插件没加载上、为什么。以前只有日志里有，界面上表现为"它不见了"
    const iss = await api('/api/tools/issues').catch(() => null)
    setToolIssues((iss && iss.issues) || [])
  }
  const loadSettings = async () => {
    const s = await api('/api/settings').catch(() => BLANK_SETTINGS)
    setSettings(s); setSetSnap(JSON.stringify(s))
  }
  // 工具/配置重新拉一遍（改完插件、工具说明后）
  const reload = async () => { await Promise.all([loadTools(), loadSettings(), loadEndpoints(), loadPersonas()]) }

  // 新建接口时"填完就检验"。
  //
  // 地址成形、并且（非本地地址的）key 也填了，就自动问一次上游 /models：
  // 通不通、有没有模型、key 对不对，当场就知道，不用等存下来再按按钮。
  // 防抖 700ms —— key 是一字一字敲的，每敲一下都发请求既慢又容易触发限流。
  useEffect(() => {
    const e = editE
    if (!e) return
    if (!e._new) {
      // 已保存的接口：改过地址或 key 之后，上一次的检验结果就不再代表它了，先清掉。
      // setProbe(null) 传的是同一个 null，React 会自己跳过，不会成环。
      const snap = editESnap ? JSON.parse(editESnap) : null
      if (snap && (snap.base_url !== e.base_url || (snap.api_key || '') !== (e.api_key || ''))) setProbe(null)
      return
    }
    const url = (e.base_url || '').trim()
    const key = (e.api_key || '').trim()
    if (!/^https?:\/\/[^\s]+$/i.test(url)) { setProbe(null); return }
    // 本机地址不要求 key（llama.cpp / llava 之类本来就没有鉴权）
    const local = /^https?:\/\/(127\.0\.0\.1|localhost|\[::1\])(:|\/|$)/i.test(url)
    if (!key && !local) { setProbe({ state: 'waitkey' }); return }
    setProbe({ state: 'running' })
    const t = setTimeout(() => { probeEndpoint(e) }, 700)
    return () => clearTimeout(t)
  }, [editE && editE._new, editE && editE.base_url, editE && editE.api_key])

  // 挂载：载入 + 处理深链
  useEffect(() => {
    // 先同步抓查询串：App 的 ?conv= 深链会 history.replaceState 把参数清掉（它也在挂载时跑）
    const q = new URLSearchParams(location.search)
    ;(async () => {          // 前面必须留分号：否则会被解析成 new URLSearchParams(...)(async ...)
      await reload(); await loadTasks(); loadShotStats()
      const pn = q.get('profile')                 // ?profile=9-exec 直接打开编辑
      if (pn) {
        const list = await api('/api/profiles').catch(() => [])
        const pr = (list || []).find(x => x.name === pn)
        if (pr) {
          const v = { ...pr, tools: (pr.tools || []).join(','), headersText: headersToText(pr.headers) }
          setEditP(v); setEditSnap(snapOf(v))     // 深链打开即"干净"，不该一进来就是脏的
          const fld = q.get('field')              // ?field=system 直接打开提示编辑
          if (fld) { setEditOriginal(pr[fld] || ''); setEditField(fld) }
        }
      }
      const tq = q.get('task')                    // ?task=3 直接看任务详情
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
        // 2 级同一时刻只能有一张：打开工具前先把模型详情清掉
        if (t) pickTool(t)
        if (t && q.get('tooledit')) { setEditOriginal(t.description || ''); setToolEdit({ name: t.name, description: t.description }) }
      }
      if (q.get('newplugin')) {
        // 不在这里直接调 newPlugin：它定义在下面（TDZ）。
        // 用一个标记，等挂载后的 effect 统一处理
        setPendingNewPlugin(true)
      }
    })()
  }, [])

  // 任务状态每 5 秒刷一次（列表里显示"N 个在跑"）
  useEffect(() => { const t = setInterval(loadTasks, 5000); return () => clearInterval(t) }, [])

  const formDirty = !!editP && snapOf(editP) !== editSnap
  const editorDirty = (editField || toolEdit)
    ? ((editField ? (editP?.[editField] || '') : (toolEdit?.description || '')) !== (editOriginal || ''))
    : false
  const settingsDirty = JSON.stringify(settings) !== setSnap
  // 接口层的脏标记，和配置层同一把尺子：快照比对，
  // 只认用户能改的字段（_new 这类元数据不算）。
  const editEDirty = !!editE && snapOf(editE) !== editESnap
  // 草稿：退出详情卡时把没保存的改动按 profile 名存这儿。
  // 目的——改完 A 想再看 B、回头一起保存；退出层级不等于放弃改动。
  const [drafts, setDrafts] = useState({})
  const draftsDirty = Object.values(drafts).some(d => snapOf(d.data) !== d.base)
  // 工具表单自己的脏标记：它有自己的保存按钮，不并进 anyDirty（那个走清单）
  const toolFormDirty = !!toolForm && toolSnap(toolForm) !== toolSnap(toolSnapShot)
  const anyDirty = formDirty || editorDirty || settingsDirty || draftsDirty || toolFormDirty || editEDirty
  // 带着未保存修改想关面板时的提示：不弹窗，每 +1 让"取消/保存"跳两下。
  // 用计数器而不是布尔，同一个动作要能重复触发（连按两次 Esc 都该跳）。
  const [nudge, setNudge] = useState(0)
  // 保存前弹出的改动清单；null = 不显示。确认后才真正写后端。
  const [sheet, setSheet] = useState(null)
  const [committing, setCommitting] = useState(false)

  // 取消：放弃**所有**攒着没存的改动。
  //
  // 这个按钮只出现在一级卡标题栏和窄屏底部操作条上，都是"整批"的位置；
  // 它旁边的保存弹的是全部改动清单，所以取消也必须是全部，否则
  // 点了取消、操作条上还挂着草稿（原来只回滚当前层，草稿没清）。
  const cancel = () => {
    setDrafts({})
    setEditField(null); setToolEdit(null); setToolForm(null); setToolSnapShot(null)
    // 快照为空 = 这张卡是"新建"，回滚等于回滚到"没这张卡"，直接关掉；
    // 回滚成 {} 会得到一张标题写着 undefined 的空卡
    if (editP) { if (editSnap) setEditP(JSON.parse(editSnap)); else setEditP(null) }
    if (editE) { if (editESnap) setEditE(JSON.parse(editESnap)); else setEditE(null) }
    setSettings(JSON.parse(setSnap || '{}'))
  }

  // 把所有"攒着没存"的改动收集成一份清单：当前打开的 profile、草稿里的其它 profile、
  // 工具说明、常规配置。保存时先给它看，确认后一次提交。
  const pendingChanges = () => {
    const out = []
    const seen = new Set()
    if (editEDirty) {
      out.push({ kind: 'endpoint', title: (editE._new ? '新接口 · ' : '接口 · ') + (editE.name || '（未命名）'),
        name: editE.name, base: JSON.parse(editESnap || '{}'), next: editE,
        rows: diffFields(JSON.parse(editESnap || '{}'), editE, ENDPOINT_LABELS) })
    }
    if (formDirty && editP) {
      out.push({ kind: 'profile', title: '模型 · ' + editP.name, name: editP.name,
        base: JSON.parse(editSnap || '{}'), next: editP, rows: diffFields(JSON.parse(editSnap || '{}'), editP, FIELD_LABELS) })
      seen.add(editP.name)
    }
    for (const [name, d] of Object.entries(drafts)) {
      if (seen.has(name) || snapOf(d.data) === d.base) continue
      out.push({ kind: 'profile', title: '模型 · ' + name, name,
        base: JSON.parse(d.base || '{}'), next: d.data,
        rows: diffFields(JSON.parse(d.base || '{}'), d.data, FIELD_LABELS) })
    }
    // 插件定义（整份表单）：和上面 profile 一样，攒着一起提交
    if (toolForm && toolFormDirty) {
      const base = toolSnapShot || {}
      out.push({
        kind: 'plugin', title: (toolForm.isNew ? '新插件 · ' : '插件 · ') + (toolForm.name || '（未命名）'),
        name: toolForm.name, next: toolForm,
        rows: diffFields(base, toolForm, TOOL_LABELS)
      })
    }
    if (toolEdit && editorDirty && !editField) {
      out.push({ kind: 'tool', title: '工具说明 · ' + toolEdit.name, name: toolEdit.name,
        base: { description: editOriginal }, next: { description: toolEdit.description },
        rows: diffFields({ description: editOriginal }, { description: toolEdit.description }, FIELD_LABELS) })
    }
    if (settingsDirty) {
      out.push({ kind: 'settings', title: '常规配置',
        base: JSON.parse(setSnap || '{}'), next: settings,
        rows: diffFields(JSON.parse(setSnap || '{}'), settings, SETTING_LABELS) })
    }
    return out
  }

  // 放弃清单里的全部改动：草稿清空、当前层回滚
  const discardAll = () => {
    setDrafts({})
    setEditP(null); setEditField(null); setToolEdit(null)
    setEditE(null); setEditM(null)
    // 插件表单有自己的保存按钮，但也进清单 —— "全部放弃"当然也包括它
    if (toolForm && toolSnapShot) setToolForm({ ...toolSnapShot })
    setSettings(JSON.parse(setSnap || '{}'))
    setSheet(null)
  }

  // 把清单真正写进后端。一项失败就停在那儿并保留清单，不假装成功。
  // 把清单一次提交：POST /api/apply，后端按 kind 分发。
  // 以前是前端循环发请求（profile 一个、工具一个、配置再一个），
  // 中途失败就停在半路、前面几条已生效。现在只有一次往返，
  // 而且后端会先全量校验再写，失败时告诉你是第几项、什么原因。
  const commitSheet = async () => {
    const list = sheet || []
    if (!list.length) return
    setCommitting(true)
    const changes = list.map(c => {
      if (c.kind === 'profile') {
        const { _new, ...body } = c.next
        return {
          kind: 'profile', name: c.next.name,
          data: {
            ...(() => { const { headersText, ...rest } = body
              return { ...rest, headers: textToHeaders(headersText) } })(),
            // 数字字段统一转一遍：输入框给的是字符串，后端按 int 解，
            // 空字符串要变成 0（"没填"），而不是让 JSON 解码直接失败
            context_window: Number(body.context_window) || 0,
            max_tokens_out: Number(body.max_tokens_out) || 0,
            max_prompt_tokens: Number(body.max_prompt_tokens) || 0,
            compact_pct: Number(body.compact_pct) || 0,
            tools: (body.tools || '').split(',').map(x => x.trim()).filter(Boolean),
            keep_messages: body.keep_messages || 16
          }
        }
      }
      if (c.kind === 'tool') return { kind: 'tool', name: c.name, data: { description: c.next.description } }
      if (c.kind === 'plugin') {
        const f = c.next
        let params = null
        try { params = f.paramsText.trim() ? JSON.parse(f.paramsText) : null } catch (e) { params = null }
        return {
          kind: 'plugin',
          data: {
            name: (f.name || '').trim(), originalName: f.originalName || '', oldName: f.oldName || '',
            category: (f.category || '').trim(),
            description: f.description || '',
            command: (f.command || '').split('\n').map(x => x.trim()).filter(Boolean),
            timeoutMs: Number(f.timeoutMs) || 20000, persistent: !!f.persistent,
            params, required: (f.requiredText || '').split(',').map(x => x.trim()).filter(Boolean)
          }
        }
      }
      if (c.kind === 'endpoint') {
        const { _new, ...body } = c.next
        return {
          kind: 'endpoint', name: (body.name || '').trim(),
          data: {
            name: (body.name || '').trim(), base_url: (body.base_url || '').trim(),
            api_key: body.api_key || '', provider: body.provider || '', source: body.source || ''
          }
        }
      }
      if (c.kind === 'model') {
        const { _new, ...body } = c.next
        return {
          kind: 'model',
          data: {
            endpoint: body.endpoint || '', id: (body.id || '').trim(),
            context_window: Number(body.context_window) || 0,
            max_tokens: Number(body.max_tokens) || 0,
            input: body.input && body.input.length ? body.input : ['text'],
            effort_levels: body.effort_levels || []
          }
        }
      }
      return { kind: 'settings', data: c.next }
    })
    let resp
    try {
      resp = await fetch('/api/apply', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ changes })
      })
    } catch (e) {
      setCommitting(false); alert('保存失败：' + e.message); return
    }
    const j = await resp.json().catch(() => ({}))
    if (!resp.ok) {
      // 后端给了 index 就指出具体是哪一条，别让人对着一句"保存失败"猜
      const which = typeof j.index === 'number' && list[j.index] ? '（' + list[j.index].title + '）' : ''
      setCommitting(false)
      alert('保存失败' + which + '：' + (j.error || ('HTTP ' + resp.status)))
      return
    }
    // 成功才清状态
    setDrafts({})
    if (editP) { setEditSnap(snapOf(editP)) }
    // 接口/模型存过之后就不是"新建"了：标题去掉"新建"、名字和 id 变成只读，
    // 否则再改一次名字会让引用它的模型/配置悬空
    if (editE) { setEditE({ ...editE, _new: false }); setEditESnap(snapOf({ ...editE, _new: false })) }
    setEditOriginal(editField ? (editP?.[editField] || '') : (toolEdit?.description || ''))
    setSetSnap(JSON.stringify(settings))
    if (toolForm) setToolSnapShot(toolForm)
    setSheet(null); setCommitting(false)
    await reload(); await refresh()
  }

  // 保存：不直接发请求，先把攒着的改动摊成清单给人过一眼，确认后统一提交
  const save = async () => {
    const list = pendingChanges()
    if (!list.length) return
    setSheet(list)
  }

  // 逐级退出：编辑器 → 详情 → 设置。
  // 这一层有未保存修改时**不关**，而是让该卡的"取消/保存"跳两下（nudge）——
  // 弹窗会打断、要读字、还要点一下；跳动是"这里有东西没存"的即时反馈，
  // 按钮就在眼前，想存还是想丢都直接点。
  const closeOne = async () => {
    const open = editField || toolEdit
    if (open) {
      // system 提示的改动落在 editP 上，退出编辑器不会丢 —— 直接退。
      // 常见用法：改完 system 还想改这个模型的别的字段，一起保存；
      // 在中间层就拦住会打断这个流程。
      // 工具说明存在 toolEdit 里，退出即丢，只有它对"退出"负责。
      if (!editField && editorDirty) { setNudge(n => n + 1); return }
      if (editField) setEditField(null); else setToolEdit(null)
      return
    }
    if (editP) {
      // 退出详情卡不再拦：脏的存成草稿，下次打开还在，可以和后面的改动一起提交
      if (formDirty) setDrafts(d => ({ ...d, [editP.name]: { data: editP, base: editSnap } }))
      setEditP(null); return
    }
    // 接口和模型没有草稿列表可续，退出就真的丢了 —— 所以这里拦住，让按钮跳两下
    if (editE) {
      if (editEDirty) { setNudge(n => n + 1); return }
      setEditE(null); return
    }
    if (settingsDirty) { setNudge(n => n + 1); return }
    setShowSet(false)
  }

  // 2 级详情同一时刻只能有一张：配置 / 接口 / 模型 / 插件表单四者互斥。
  // 以前每个"打开"函数各抄一遍要清哪些，加一层就漏一层（打开接口卡再点工具，
  // 屏幕上就并排两张 2 级卡）。集中在这儿，加层只改这一处。
  const clearDetail = () => {
    setEditP(null); setEditE(null)
    setToolForm(null); setToolSnapShot(null); setToolEdit(null); setEditField(null)
  }

  // —— 列表卡上的各种"打开"——
  const openProfile = (pr) => {
    clearDetail()
    // 有草稿就续上：用户上次退出时改过但没保存，再打开应该看到自己改的版本
    const d = drafts[pr.name]
    if (d) { setEditP(d.data); setEditSnap(d.base) }
    else {
      const v = { ...pr, tools: (pr.tools || []).join(','), headersText: headersToText(pr.headers) }
      setEditP(v); setEditSnap(snapOf(v))
    }
  }
  const newProfile = () => {
    clearDetail()
    setEditP({ ...NEW_PROFILE }); setEditSnap('')
  }
  // —— 接口（地址 + 凭据）——
  const pickEndpoint = (e) => {
    clearDetail()
    setEditE({ ...e }); setEditESnap(snapOf(e)); setProbe(null)
  }
  const newEndpoint = () => {
    clearDetail()
    const e = { name: '', base_url: '', api_key: '', provider: '', source: '', _new: true }
    setEditE(e); setEditESnap(''); setProbe(null)   // 新建卡天生是脏的（本来就有东西要存）
  }
  const closeEndpoint = () => {
    if (editEDirty) { setNudge(n => n + 1); return }
    setEditE(null); setProbe(null)
  }
  const deleteEndpoint = async () => {
    if (!editE) return
    if (!confirm('删除接口 ' + editE.name + '？')) return
    const r = await fetch('/api/endpoints/' + encodeURIComponent(editE.name), { method: 'DELETE' })
    const j = await r.json().catch(() => ({}))
    if (!r.ok) { alert(j.error || '删除失败'); return }
    setEditE(null); setProbe(null); await loadEndpoints(); await refresh()
  }
  // 拿**当前草稿**的地址和 key 去问一次上游 /models（不落盘）。
  //
  // 两处用它：新建接口填完 url(+key) 自动跑一次；「更新信息」按钮手动跑。
  // 以前只有"先存下来，再按一个按钮拉列表"，于是 key 打错要等存完才知道。
  const probeEndpoint = async (draft) => {
    const e = draft || editE
    if (!e) return null
    const url = (e.base_url || '').trim()
    if (!url) return null
    const r = await api('/api/endpoints/probe', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ base_url: url, api_key: e.api_key || '', provider: e.provider || '' })
    }).catch(() => ({ error: '请求没发出去（后端没响应）' }))
    const next = r.error
      ? { state: 'error', error: r.error, url }
      : { state: 'ok', models: r.models || [], url }
    setProbe(next)
    return r
  }

  // —— 模型列表（懒加载）：配置卡里"选模型"时才去问上游 ——
  //
  // 只有"有哪些模型、各自什么能力"这一件事需要联网，而且只在打开下拉时才需要。
  // 拿到之后：选中一个 → 能力填进 profile（之后读配置就完全不依赖网络）；
  // 列表本身存进 modelList，供离线时仍能选上次见过的。
  const loadModelList = async (endpointName, force = true) => {
    if (!endpointName) return null
    const prev = modelList[endpointName]
    if (!force && prev && !prev.error) return prev
    setModelList(m => ({ ...m, [endpointName]: { ...(prev || {}), loading: true } }))
    const r = await api('/api/endpoints/' + encodeURIComponent(endpointName) + '/models').catch(() => null)
    if (!r) {
      setModelList(m => ({ ...m, [endpointName]: { ...(prev || {}), loading: false, error: '后端没响应' } }))
      return null
    }
    const next = {
      loading: false, models: r.models || [], at: r.at || null,
      stale: !!r.stale, error: r.error || ''
    }
    setModelList(m => ({ ...m, [endpointName]: next }))
    return next
  }

  // 点工具直接打开**可编辑表单**（和模型配置一样，没有独立的只读态）。
  //
  // 内置工具也走同一张表单，只是 editable 为 false —— 命令、超时、常驻这些
  // 由程序决定，用 .ro 灰掉；说明两边都能改（点它进 3 级长文本）。
  // 这样两种工具的布局、字段顺序、间距完全一致，差别只剩"哪些格子能点"。
  const pickTool = (t) => {
    clearDetail()
    const isPlugin = t.source === 'plugin'
    // 原名取自清单路径的文件名 —— 改过名之后它会和显示名分叉
    const orig = (t.path || '').split('/').pop().replace(/\.json$/, '')
    const form = {
      isNew: false, source: t.source || 'builtin', path: t.path || '',
      name: t.name, originalName: orig || t.name, oldName: t.name,
      category: t.category || '',
      description: t.description || '',
      command: (t.command || []).join('\n'),
      timeoutMs: t.timeout_ms ? String(t.timeout_ms) : '',
      persistent: !!t.persistent,
      paramsText: JSON.stringify(t.params || {}, null, 2),
      requiredText: (t.required || []).join(',')
    }
    setToolForm(form); setToolSnapShot(form)
  }
  const newPlugin = () => {
    clearDetail()
    const form = { ...NEW_PLUGIN, source: 'plugin' }
    setToolForm(form); setToolSnapShot(form)
  }
  const closeToolForm = () => {
    if (toolFormDirty) { setNudge(n => n + 1); return }
    setToolForm(null); setToolSnapShot(null)
  }
  // 深链 ?newplugin 的处理：等 newPlugin 定义好再执行，避免 TDZ 和漏设快照
  useEffect(() => {
    if (!pendingNewPlugin) return
    setPendingNewPlugin(false)
    newPlugin()
  }, [pendingNewPlugin])
  const pickTask = async (t) => {
    const d = await api('/api/tasks/' + t.id).catch(() => null)
    setTaskDetail(prev => (prev && prev.task && prev.task.id === t.id) ? null : d)
  }
  const openSystemEditor = () => { setEditOriginal(editP?.system || ''); setEditField('system'); setToolEdit(null) }
  const openToolEditor = (name, desc) => { setEditOriginal(desc || ''); setToolEdit({ name, description: desc }); setEditField(null) }

  // —— 插件/模型的写操作 ——
  const deletePlugin = async () => {
    // 详情卡和编辑表单里都可能点删除：名字优先取表单，其次取详情
    const name = toolForm?.name
    if (!name) return
    if (!confirm('删除插件 ' + name + '？（只删清单，脚本文件保留）')) return
    const r = await fetch('/api/plugins/' + name, { method: 'DELETE' })
    const j = await r.json().catch(() => ({}))
    if (!r.ok) { alert(j.error || '删除失败'); return }
    setToolForm(null); await refresh(); await reload()
  }
  const deleteProfile = async () => {
    if (!editP || !confirm('删除 ' + editP.name + '？')) return
    await fetch('/api/profiles/' + editP.name, { method: 'DELETE' })
    setEditP(null); setEditField(null); await refresh()
  }
  const cleanShots = async () => {
    const r = await fetch('/api/shots/cleanup', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ days: settings.shots_keep_days || 7 }) }).then(x => x.json())
    setShotInfo(`已清理 ${r.removed} 张，释放 ${r.freed_kb} KB`)
    loadShotStats()
  }

  return {
    showSet, setShowSet,
    editP, setEditP, editField, setEditField, editOriginal, editSnap,
    toolList, toolIssues, personas, toolForm, setToolForm, toolFormDirty, toolEdit, setToolEdit,
    settings, setSettings, tasks, recipes, taskActive, taskDetail, setTaskDetail,
    shotInfo,
    formDirty, editorDirty, settingsDirty, anyDirty, nudge,
    drafts, draftsDirty, sheet, setSheet, committing, commitSheet, discardAll, pendingChanges,
    save, cancel, closeOne, reload, loadTasks, loadShotStats,
    openProfile, newProfile, pickTool, newPlugin, closeToolForm, pickTask,
    endpoints, sources, editE, setEditE, editESnap, editEDirty, probe,
    pickEndpoint, newEndpoint, closeEndpoint, deleteEndpoint, probeEndpoint,
    modelList, loadModelList,
    openSystemEditor, openToolEditor,
    deletePlugin, deleteProfile, cleanShots,
  }
}
