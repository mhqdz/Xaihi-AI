// 设置面板的容器：状态机 + 保存/取消/关闭 + 深链（?profile=&field=&tool=&tooledit=&newplugin=&task=）
// 展示组件只拿算好的值，不放业务逻辑在这里之外。
import { useEffect, useRef, useState } from 'react'

const api = (p, opt) => fetch(p, opt).then(r => r.json())

// 快照去掉内部标记 _new，避免"一打开就算脏"✗
export const snapOf = (p) => { if (!p) return ''; const { _new, ...rest } = p; return JSON.stringify(rest) }

const BLANK_SETTINGS = { tool_rounds_enabled: true, tool_rounds: 8 }
const NEW_PROFILE = {
  _new: true, name: '', base_url: 'http://127.0.0.1:18320/180/v1', api_key: '',
  model: 'qwen3.8-flash-next-iq3_xxs', reasoning_effort: 'high', tools: '', system: ''
}
const NEW_PLUGIN = {
  isNew: true, name: '', description: '', command: '', timeoutMs: 20000,
  persistent: false, paramsText: '{\n  "query": { "type": "string" }\n}', requiredText: 'query'
}


// 改动清单里的字段名。后端字段直白，但"上下文窗口"比 context_window 好认。
const FIELD_LABELS = {
  name: '名称', base_url: '接口地址', api_key: 'API Key', model: '模型',
  reasoning_effort: '思考强度', tools: '可用工具', system: 'system 提示',
  keep_messages: '保留消息数', context_window: '上下文窗口', compact_pct: '压缩阈值'
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
  const [toolCfg, setToolCfg] = useState(null)    // 工具卡（只读信息）
  const [toolForm, setToolForm] = useState(null)  // 插件表单（新建/编辑）
  const [toolEdit, setToolEdit] = useState(null)  // 工具说明的三级编辑
  const [toolIssues, setToolIssues] = useState([])  // 加载失败被跳过的插件及原因
  const [settings, setSettings] = useState(BLANK_SETTINGS)
  const [setSnap, setSetSnap] = useState(() => JSON.stringify(BLANK_SETTINGS))
  const [tasks, setTasks] = useState([])
  const [recipes, setRecipes] = useState([])
  const [taskActive, setTaskActive] = useState(0)
  const [taskDetail, setTaskDetail] = useState(null)
  const [shotInfo, setShotInfo] = useState('')
  const [models] = useState({ loading: false, list: [], err: '' })

  const loadTasks = async () => {
    const r = await api('/api/tasks').catch(() => null)
    if (r && !Array.isArray(r)) { setTasks(r.tasks || []); setRecipes(r.recipes || []); setTaskActive(r.active || 0) }
  }
  const loadShotStats = async () => {
    const r = await api('/api/shots/stats').catch(() => null)
    if (r) setShotInfo(`当前 ${r.count} 张，占用 ${r.size_mb.toFixed(1)} MB`)
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
  const reload = async () => { await Promise.all([loadTools(), loadSettings()]) }

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
          const v = { ...pr, tools: (pr.tools || []).join(',') }
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
        if (t) { setEditP(null); setToolCfg({ ...t, enabled: true, subtools: [] }) }   // 2 级详情只能有一张
        if (t && q.get('tooledit')) { setEditOriginal(t.description || ''); setToolEdit({ name: t.name, description: t.description }) }
      }
      if (q.get('newplugin')) { setEditP(null); setToolForm({ ...NEW_PLUGIN }) }
    })()
  }, [])

  // 任务状态每 5 秒刷一次（列表里显示"N 个在跑"）
  useEffect(() => { const t = setInterval(loadTasks, 5000); return () => clearInterval(t) }, [])

  const formDirty = !!editP && snapOf(editP) !== editSnap
  const editorDirty = (editField || toolEdit)
    ? ((editField ? (editP?.[editField] || '') : (toolEdit?.description || '')) !== (editOriginal || ''))
    : false
  const settingsDirty = JSON.stringify(settings) !== setSnap
  // 草稿：退出详情卡时把没保存的改动按 profile 名存这儿。
  // 目的——改完 A 想再看 B、回头一起保存；退出层级不等于放弃改动。
  const [drafts, setDrafts] = useState({})
  const draftsDirty = Object.values(drafts).some(d => snapOf(d.data) !== d.base)
  const anyDirty = formDirty || editorDirty || settingsDirty || draftsDirty
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
    setEditField(null); setToolEdit(null)
    if (editP) setEditP(JSON.parse(editSnap || '{}'))
    setSettings(JSON.parse(setSnap || '{}'))
  }

  // 把所有"攒着没存"的改动收集成一份清单：当前打开的 profile、草稿里的其它 profile、
  // 工具说明、常规配置。保存时先给它看，确认后一次提交。
  const pendingChanges = () => {
    const out = []
    const seen = new Set()
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
            ...body,
            tools: (body.tools || '').split(',').map(x => x.trim()).filter(Boolean),
            keep_messages: body.keep_messages || 16
          }
        }
      }
      if (c.kind === 'tool') return { kind: 'tool', name: c.name, data: { description: c.next.description } }
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
    setEditOriginal(editField ? (editP?.[editField] || '') : (toolEdit?.description || ''))
    setSetSnap(JSON.stringify(settings))
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
    if (settingsDirty) { setNudge(n => n + 1); return }
    setShowSet(false)
  }

  // —— 列表卡上的各种"打开"——
  const openProfile = (pr) => {
    // 有草稿就续上：用户上次退出时改过但没保存，再打开应该看到自己改的版本
    const d = drafts[pr.name]
    if (d) { setEditP(d.data); setEditSnap(d.base) }
    else {
      const v = { ...pr, tools: (pr.tools || []).join(',') }
      setEditP(v); setEditSnap(snapOf(v))
    }
    setEditField(null); setToolEdit(null); setToolCfg(null)
  }
  const newProfile = () => {
    setEditP({ ...NEW_PROFILE }); setEditSnap(''); setEditField(null); setToolEdit(null)
    setToolCfg(null); setToolForm(null)   // 2 级详情同一时刻只能有一张
  }
  // 点工具只打开"详情卡"（内置和外置同一套）。以前外置直接跳进编辑表单，
  // 而列表接口当时不返回 command —— 于是给你一张命令为空的表单，
  // 一保存就被"名字与命令必填"拦下。信息先看，要改再点编辑。
  const pickTool = (t) => {
    setEditP(null); setToolEdit(null); setEditField(null); setToolForm(null)
    setToolCfg({ ...t, enabled: t.enabled !== false, subtools: t.subtools || [] })
  }

  // 从详情卡进入插件定义编辑（只有外置有）
  const editPlugin = () => {
    const t = toolCfg
    if (!t) return
    // 原名取自清单路径的文件名 —— 它不是 t.name：改名后文件名会保持不变
    const orig = (t.path || '').split('/').pop().replace(/\.json$/, '')
    setToolForm({
      isNew: false, name: t.name, originalName: orig || t.name, oldName: t.name, description: t.description || '',
      command: (t.command || []).join('\n'), timeoutMs: t.timeout_ms || 20000,
      persistent: !!t.persistent, paramsText: JSON.stringify(t.params || {}, null, 2),
      requiredText: (t.required || []).join(',')
    })
  }
  const newPlugin = () => {
    setEditP(null); setToolEdit(null); setEditField(null); setToolCfg(null)
    setToolForm({ ...NEW_PLUGIN })
  }
  const closeToolForm = () => { setToolForm(null); setToolCfg(null) }
  const pickTask = async (t) => {
    const d = await api('/api/tasks/' + t.id).catch(() => null)
    setTaskDetail(prev => (prev && prev.task && prev.task.id === t.id) ? null : d)
  }
  const openSystemEditor = () => { setEditOriginal(editP?.system || ''); setEditField('system'); setToolEdit(null) }
  const openToolEditor = (name, desc) => { setEditOriginal(desc || ''); setToolEdit({ name, description: desc }); setEditField(null) }

  // —— 插件/模型的写操作 ——
  const savePlugin = async () => {
    if (!toolForm) return
    if (!toolForm.name || !toolForm.command.trim()) { alert('名字与命令必填'); return }
    let params = null
    try { params = toolForm.paramsText.trim() ? JSON.parse(toolForm.paramsText) : null } catch { alert('参数不是合法 JSON'); return }
    const body = {
      name: toolForm.name.trim(), originalName: toolForm.originalName || '', oldName: toolForm.oldName || '',
      description: toolForm.description || '',
      command: toolForm.command.split('\n').map(x => x.trim()).filter(Boolean),
      timeoutMs: Number(toolForm.timeoutMs) || 20000, persistent: !!toolForm.persistent,
      params, required: (toolForm.requiredText || '').split(',').map(x => x.trim()).filter(Boolean)
    }
    const r = await fetch('/api/plugins', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    const j = await r.json().catch(() => ({}))
    if (!r.ok) { alert(j.error || '保存失败'); return }
    // 编辑已有插件：保存后回到详情卡（并换成新数据），别把整层关掉；
    // 新建则退回列表，让新插件出现在工具列表里
    setToolForm(null)
    if (toolForm.isNew) {
      setToolCfg(null)
    } else {
      const list = await api('/api/tools').catch(() => null)
      const updated = (list || []).find(t => t.name === body.name)
      setToolCfg(updated ? { ...updated, enabled: true, subtools: [] } : null)
    }
    await refresh(); await reload()
  }
  const deletePlugin = async () => {
    // 详情卡和编辑表单里都可能点删除：名字优先取表单，其次取详情
    const name = toolForm ? toolForm.name : toolCfg?.name
    if (!name) return
    if (!confirm('删除插件 ' + name + '？（只删清单，脚本文件保留）')) return
    const r = await fetch('/api/plugins/' + name, { method: 'DELETE' })
    const j = await r.json().catch(() => ({}))
    if (!r.ok) { alert(j.error || '删除失败'); return }
    setToolForm(null); setToolCfg(null); await refresh(); await reload()
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
    toolList, toolIssues, toolCfg, setToolCfg, toolForm, setToolForm, toolEdit, setToolEdit,
    settings, setSettings, tasks, recipes, taskActive, taskDetail, setTaskDetail,
    models, shotInfo,
    formDirty, editorDirty, settingsDirty, anyDirty, nudge,
    drafts, draftsDirty, sheet, setSheet, committing, commitSheet, discardAll, pendingChanges,
    save, cancel, closeOne, reload, loadTasks, loadShotStats,
    openProfile, newProfile, pickTool, newPlugin, closeToolForm, pickTask,
    openSystemEditor, openToolEditor,
    savePlugin, deletePlugin, deleteProfile, cleanShots, editPlugin,
  }
}
