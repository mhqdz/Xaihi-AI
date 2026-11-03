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
  const loadTools = async () => { setToolList((await api('/api/tools').catch(() => [])) || []) }
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
  const anyDirty = formDirty || editorDirty || settingsDirty

  // 取消：只回滚当前层
  const cancel = () => {
    if (editorDirty) {
      if (editField) setEditP({ ...editP, [editField]: editOriginal })
      else setToolEdit({ ...toolEdit, description: editOriginal })
    } else if (formDirty) {
      setEditP(JSON.parse(editSnap || '{}'))
    } else {
      setSettings(JSON.parse(setSnap || '{}'))
    }
  }

  // 保存：三级编辑器 → profile → 常规配置
  const save = async () => {
    if (editorDirty) {
      if (toolEdit) {
        await fetch('/api/tools/' + toolEdit.name, { method: 'PUT', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ description: toolEdit.description }) })
        setEditOriginal(toolEdit.description || ''); await reload()
      } else { setEditOriginal(editP?.[editField] || '') }
      return
    }
    if (formDirty) {
      if (!editP.name || !editP.base_url || !editP.model) { alert('名称 / 接口地址 / 模型 必填'); return }
      const { _new, ...body } = editP
      await fetch('/api/profiles', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...body, tools: (body.tools || '').split(',').map(x => x.trim()).filter(Boolean),
          keep_messages: body.keep_messages || 16 }) })
      setEditSnap(snapOf(editP)); await refresh(); return
    }
    await fetch('/api/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(settings) })
    setSetSnap(JSON.stringify(settings))
  }

  // 逐级退出：编辑器 → 详情 → 设置；有改动先问是否保存
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
          await reload()
        }
      }
      if (editField) setEditField(null); else setToolEdit(null)
      return
    }
    // 详情卡 / 设置层：原来直接关，脏了也一样 —— 改完参数按 Esc 或点 ×，
    // 面板消失、服务端没变、一句提示都没有。编辑器那层有确认，这两层没有。
    if (editP) {
      if (formDirty && !confirm('模型配置有未保存的修改，确定放弃？')) return
      setEditP(null); return
    }
    if (anyDirty && !confirm('设置有未保存的修改，确定放弃？')) return
    setShowSet(false)
  }

  // —— 列表卡上的各种"打开"——
  const openProfile = (pr) => {
    const v = { ...pr, tools: (pr.tools || []).join(',') }
    setEditP(v); setEditSnap(snapOf(v)); setEditField(null); setToolEdit(null); setToolCfg(null)
  }
  const newProfile = () => {
    setEditP({ ...NEW_PROFILE }); setEditSnap(''); setEditField(null); setToolEdit(null)
    setToolCfg(null); setToolForm(null)   // 2 级详情同一时刻只能有一张
  }
  const pickTool = (t) => {
    setEditP(null); setToolEdit(null); setEditField(null)
    setToolCfg({ ...t, enabled: t.enabled !== false, subtools: t.subtools || [] })
    setToolForm(t.source === 'plugin' ? {
      isNew: false, name: t.name, description: t.description || '',
      command: (t.command || []).join('\n'), timeoutMs: t.timeout_ms || 20000,
      persistent: !!t.persistent, paramsText: JSON.stringify(t.params || {}, null, 2),
      requiredText: (t.required || []).join(',')
    } : null)
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
      name: toolForm.name.trim(), description: toolForm.description || '',
      command: toolForm.command.split('\n').map(x => x.trim()).filter(Boolean),
      timeoutMs: Number(toolForm.timeoutMs) || 20000, persistent: !!toolForm.persistent,
      params, required: (toolForm.requiredText || '').split(',').map(x => x.trim()).filter(Boolean)
    }
    const r = await fetch('/api/plugins', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    const j = await r.json().catch(() => ({}))
    if (!r.ok) { alert(j.error || '保存失败'); return }
    setToolForm(null); setToolCfg(null); await refresh(); await reload()
  }
  const deletePlugin = async () => {
    if (!toolForm) return
    if (!confirm('删除插件 ' + toolForm.name + '？（只删清单，脚本文件保留）')) return
    const r = await fetch('/api/plugins/' + toolForm.name, { method: 'DELETE' })
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
    toolList, toolCfg, setToolCfg, toolForm, setToolForm, toolEdit, setToolEdit,
    settings, setSettings, tasks, recipes, taskActive, taskDetail, setTaskDetail,
    models, shotInfo,
    formDirty, editorDirty, settingsDirty, anyDirty,
    save, cancel, closeOne, reload, loadTasks, loadShotStats,
    openProfile, newProfile, pickTool, newPlugin, closeToolForm, pickTask,
    openSystemEditor, openToolEditor,
    savePlugin, deletePlugin, deleteProfile, cleanShots,
  }
}
