# 前端全面优化 · 交接包

> 用途：**下一轮（会话压缩后）读这一份就能直接开工**。包含备份、现状、功能清单、接口清单、人物卡、优化候选、验证方法与开工清单。
> 生成：2026-10-30 20:15 ｜ 代码版本：`5c2a9dc` ｜ 本轮**没有改动任何前端代码**（只做备份和这份文档）。

## 0. 一句话状态

```
xaihi 前端（React 18 + Vite）已完成三轮重构：流式缓冲出 React（streamStore）、
消息卡/ToolGroup/SysNote 拆组件并 memo、设置面板拆 5 张卡 + useSettings。
App.jsx 1689 → 1096 行；8 个 WebDriver 套件全绿；本轮结束在"可以开始做体验级优化"的干净基线上。
```

---

## 1. 备份与回滚

```
代码：git tag backup-web-20261030-2011  →  5c2a9dc
      历史备份 tag backup-web-20261030（3e827b8，Step 1 完成时）
文件：~/backups/xaihi-web-20261030-2011.tgz   (5.3 MB / 146 项)
      排除：node_modules、web/dist、.git、*.log
      旧的：~/backups/xaihi-web-20261030-0445.tgz

回滚前端源码：
  git checkout backup-web-20261030-2011 -- web/src web/public
重建：
  cd web && npm run build            # 产物写 web/dist，xaihi 直接从磁盘读
整库回滚（含后端）：
  git reset --hard backup-web-20261030-2011
  # 或解包：tar xzf ~/backups/xaihi-web-20261030-2011.tgz -C ~/Workspace/xaihi
```

**重要**：xaihi 用 `http.FileServer` 直接读 `web/dist`，所以**只改前端不需要重启服务**，`npm run build` 后刷新页面即可（html 带 `Cache-Control: no-store`）。改 Go 代码才需要：`go build -o /tmp/xaihi.new ./cmd/xaihi && systemctl --user stop xaihi && cp /tmp/xaihi.new ~/.local/bin/xaihi && systemctl --user start xaihi`。

---

## 2. 运行环境

```
生产：systemd --user 单元 xaihi.service
      Environment=AGENTD_ADDR=0.0.0.0:18400     → http://127.0.0.1:18400、http://192.168.1.200:18400
      静态目录：xaihi 仓库 web/dist（/、/assets/*、/fonts/*、/shots/*）
开发：xaihi-web-dev.service → vite 127.0.0.1:5173，proxy /api → 127.0.0.1:18400
      注意：HMR 只有客户端插件在跑；普通前端改动要 build 后在 18400 验证
依赖：web/package.json（react 18.3、vite 5.4、marked、dompurify、@number-flow/react）
构建：cd web && npm run build（vite build，无网络需求；装包才需要代理 127.0.0.1:7897）
数据：~/.data/xaihi/xaihi.db（SQLite WAL）、shots/、tools.d/、tasks.d/、profiles.json
```

---

## 3. 前端现状

```
web/src/
  App.jsx                 1096 行  布局（drawer/thread/composer/rail）+ URL 深链 + 装配
  theme.css               1172 行  全部视觉规则（Endfield 冷灰青，无 !important）
  components/
    Markdown.jsx            41     Md / Sections（marked + DOMPurify）
    MessageCard.jsx         52     用户/Agent 气泡（memo：m/seq/vidx/onCollapse）
    ToolGroup.jsx           71     工具组（默认收拢，memo 逐字段比 meta）
    SysNote.jsx             17     系统提示（定时唤醒/交接/停止说明）
    Settings/Card.jsx       19     所有设置卡共用外壳
    Settings/Panel.jsx      27     4 张卡的装配 + 列宽档位 + 唯一 is-top
    Settings/ListCard.jsx  148     常规配置 + 模型/工具/任务列表
    Settings/ModelForm.jsx  86     2 级：模型详情/新建
    Settings/ToolCard.jsx   92     2 级：工具详情/插件表单
    Settings/EditorCard.jsx 23     3 级：system / 工具说明
    Settings/DirtyActions.jsx 13   取消+保存
    Settings/XIcon.jsx      10
  conversation/
    streamStore.js          23     流式缓冲（useSyncExternalStore，window.__stream 可调试）
    messageGroups.js        46     isGhostMsg/neighborRole/runStart/runStats/thinkBefore
  settings/useSettings.js  237     设置状态机（脏检查/保存/取消/深链/插件增删改）
```

数据流：

```
POST /api/chat（SSE）
  ├─ delta/reasoning → streamStore.push（80ms 节流）→ 只有 <LiveBubble/> 重渲染
  ├─ tool            → 定稿一条 assistant + 一条 tool 进 msgs，清理缓冲
  ├─ stage/usage     → 状态栏、右栏指标
  └─ done            → 重载会话（normalize）→ 全量 MessageCard 重渲染一次

GET /api/conv/<id>  → msgs（历史，不可变对象）→ MessageCard/ToolGroup/SysNote（memo 命中）
```

性能实测（conv 51：575 条消息 / 295 条工具 / 174 个工具组）：

```
挂载       MessageCard 204 次、SysNote 10 次、ToolGroup 295 次（= 工具行数）
展开工具组  MessageCard 0 次，ToolGroup 2 次（该组两行）
完整一轮    MessageCard 3 次（新用户卡 + 新回复 + 一次重载）
调试钩子    window.__renders = {msg, tool, note}   window.__stream
```

---

## 4. 功能清单（现状）

### 4.1 抽屉（左，250px，可拖 200–520，竖屏变浮层）

```
顶部三格数字：会话数 / 归档数 / 本轮最大 seq（number-flow 逐位滚动 + 入场动画）
列表：No.0100 + 最后活跃时间 + 标题 + 条数；hover 出现归档/恢复按钮
子会话：父会话下面缩进 + 左侧竖线；第一行 #N（子序号），时间位改成真实编号 No.xxxx
底部：＋新会话（大方按钮）、设置（单行、深色）、WORKSPACE / AGENT
交互：竖屏选完会话自动收起；宽屏记住展开态（localStorage sideOpen）
```

### 4.2 正文（thread）

```
空状态：LOCAL AGENT CONSOLE + 标题 + START HERE 三步卡 + 计数 tips；不显示列宽拖动线
消息：
  用户/Agent 卡片（头像、名称、插话标、No. 编号、时间、复制；思考折叠；markdown 分段编号）
  工具组：Tool 头（▸/▾、编号区间、思考段数、条数、字数）默认收拢；展开后逐行显示
          编号/工具名/耗时/字数 + 参数 JSON + 单个工具的思考 + 输出折叠；四角括号
  系统提示：无卡片，[No.xxx] + 文字 + 尾部细线（定时唤醒、交接卡、停止原因）
  执行中：pendingTools 卡（工具名 + 参数 + 计时）
  实时气泡：LiveBubble（生成中…，思考默认展开，无 token 时"等待模型响应…"）
滚动：贴底才跟随（rAF 每帧校正）；上翻立即解除；≤8px 回到底部恢复
阅读位置：顶部 2px 进度条 = 阅读位置 / 可见条数；右栏数字同步
右侧 peek：悬停右栏数字时浮出 5 条消息缩略，滚轮切换、点击跳转
```

### 4.3 输入区（composer）

```
PROFILE 下拉 + 当前模型名（右对齐）
文本域：默认 92px（竖屏 52px），右下角拖拽调高，多行内滚
Enter 发送 / Shift+Enter 换行；生成中 Enter=插话，无文字 Enter=中止
按钮四态：send → send loading（转圈+disabled，等后端接上）→ stop/interject → send
hint：Enter 发送 · Shift+Enter 换行　#<会话 id 前 8 位>
状态栏：压缩中… / 生成中…+ 轮播语录 / 完成 · 3.3s / 已停止 / 已同步
```

### 4.4 右栏（≥1400px）与窄屏顶栏（<1400px）

```
宽：竖排 AGENT EXECUTE · CREATE / 本轮 04 / 08 / ＋新会话 / ⋮⋮设置 / ●运行中 · tok/s · 上下文%
窄：同一组指标水平搬到顶部（进度条上方），正文占满宽度
上下文：常态只显比例，悬停看 X/Yk tokens（已压缩）· 原始 Zk · 窗口 Nk；达阈值加深
```

### 4.5 设置面板（点 ⋮⋮ 或抽屉底部）

```
1 级 ListCard：常规配置（工具轮次上限 / 自动归档 / 界面·运行中提示 / 截图清理与立即清理）
              模型配置（点开 2 级详情；＋新建 profile）
              工具配置（内置 / 外置插件；点开 2 级详情或插件表单；＋新建插件）
              任务（状态徽标、子会话号、时间/错误；点开步骤、打开子会话、执行收尾）
2 级 col-detail：模型详情（名称/地址/key/模型/思考强度/服务商/窗口/输入上限/压缩阈值/工具多选/
                system 入口/删除）｜工具详情（说明入口/来源/清单/超时/启用情况）
                或插件表单（名称/说明/命令/超时/常驻开关/参数 JSON/必填/删除）
3 级 col-editor：system 提示 或 工具说明（填满高度、字数、取消/保存）
脏检查：编辑器 → profile → 常规配置，最内层优先；窄屏把保存/取消放当前卡头
深链：?settings=1&conv=…&profile=…&field=system ｜ &tool=bash&tooledit=1 ｜ &newplugin=1 ｜ &task=3
```

### 4.6 前端专属配置（不进后端）

```
web/public/ui.json       运行中轮播语录（quips）默认值；构建时复制到 dist
localStorage['ui.quips'] 设置面板里改的语录（一行一条，覆盖 ui.json；脏数据不会白屏）
localStorage 其它键：profile / sideOpen / sideW / colW / inputH
```

---

## 5. 接口清单

### 5.1 HTTP（全部 JSON；错误 `{"error":"…"}`；**无鉴权**，仅可信网络）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | `{ok, base_url, model}`（当前连的上游） |
| GET | `/api/running` | 正在生成的会话 id 数组 `["8","9"]` |
| GET | `/api/convs` | 未归档列表 `[{ID,Title,UpdatedAt,N,ParentID,Kind,Seq}]` |
| GET | `/api/convs?archived=1` | 归档列表 |
| GET | `/api/conv/{id}` | `{id,title,created_at,updated_at,messages:[…]}` |
| DELETE | `/api/conv/{id}` | 归档（`POST …/archive` 同效） |
| POST | `/api/conv/{id}/restore` | 恢复 |
| POST | `/api/conv/{id}/interject` | `{text}` 插话 |
| POST | `/api/conv/{id}/stop` | 中断当前生成（已生成内容落库） |
| GET | `/api/conv/{id}/stream?from=N` | 续传流；不在跑时 200 + 空 `done` |
| POST | `/api/chat` | `{conv_id,text,profile}` → SSE（见 5.2） |
| GET | `/api/settings` | `{tool_rounds_enabled,tool_rounds,auto_archive_enabled,auto_archive_days,shots_auto_clean,shots_keep_days}` |
| PUT | `/api/settings` | 同上结构；归档/截图字段变化立即巡检 |
| GET | `/api/context?conv=&profile=` | `{chars,max,est_tokens,limit_tokens,window,chars_per_token,compact_pct,compact_at,raw,messages,summary,pct}` |
| GET | `/api/models?base_url=&api_key=` | 代理上游 `/v1/models` → `{models:[…]}` |
| GET | `/api/profiles` | profile 数组（字段见 5.3） |
| PUT | `/api/profiles/{name}` | 新建/更新（api_key 空则回落全局） |
| DELETE | `/api/profiles/{name}` | 删除（内置也可删） |
| GET | `/api/tools` | `[{name,description,params,source,path,timeout_ms,persistent}]` |
| PUT | `/api/tools/{name}` | `{description}` 改说明（内置写覆盖，外置改清单） |
| POST/PUT | `/api/plugins` | 保存插件清单（同名内置拒绝） |
| DELETE | `/api/plugins/{name}` | 删清单（脚本保留） |
| GET | `/api/tasks` | `{tasks:[…],recipes:[…],active:N}` |
| POST | `/api/tasks` | `{kind,conv_id,card}` 手动派发 → `{ok,task_id}` |
| GET | `/api/tasks/{id}` | `{task,steps,outbox}` |
| POST | `/api/tasks/{id}/restore` | 收尾（中断任务） |
| POST | `/api/wake` | 内部：`X-Xaihi-Token` + `{conv_id,text,origin}` → 写 outbox（token 在 `~/.data/xaihi/wake.token`） |
| GET | `/api/shots/stats` | `{count,size_mb}` |
| POST | `/api/shots/cleanup` | `{days}` → `{removed,freed_kb,days}` |
| GET | `/shots/<name>.png` | 截图文件（`~/.data/xaihi/shots/`） |

### 5.2 SSE 事件（`/api/chat` 与 `/api/conv/{id}/stream`）

| event | data | 前端用途 |
|---|---|---|
| `meta` | `{conv_id,title}` | 写 URL、标题 |
| `reasoning` | `{text}` | LiveBubble 思考增量 |
| `delta` | `{text}` | LiveBubble 正文增量（80ms 节流） |
| `stage` | `{stage:"compacting"\|"generating"}` | 状态栏"压缩中…/生成中…" |
| `tool_start` | `{name,args}` | 插入"执行中…"卡 + 计时 |
| `tool` | `{name,args,text,image,ms}` | 定稿该轮 assistant + 工具行 |
| `interject` | `{text}` | 状态栏回显 |
| `usage` | `{tps,completion,prompt,cached}` | 右栏 tok/s |
| `error` | `{error}` | 半截内容 + 中断说明 |
| `done` | `{conv_id,secs}` | 收尾、重载会话 |

流期间每 5s 一条 `: ping` 注释帧。

### 5.3 关键数据结构

```
消息（GET /api/conv/<id>.messages[]）：
  {seq, role:user|assistant|tool, content, reasoning, reasoning_ms, tool_calls,
   tool_call_id, tool_result, images:[url], created_at, origin, tool_ms}
  前端 normalize 后：tool 消息补 toolName/toolArgs/ms；assistant 去掉内联 tools

profile（/api/profiles[]）：
  {name, base_url, api_key, model, provider:"", system, tools:[], keep_messages:16,
   max_prompt_tokens:105000, context_window:0, compact_pct:80, max_tokens_out:8192,
   reasoning_budget:4096, reasoning_effort, summarizer}

任务（/api/tasks.tasks[]）：
  {id, kind, status:running|done|error|interrupted, parent_conv, child_conv,
   error, created_at, finished_at}；步骤 {seq,name,cmd,status,exit_code,ms,output}

工具：内置 bash / screenshot / recall / view_image / spawn_task（handoff_fail 只在子会话）
      外置 ~/.data/xaihi/tools.d/*.json：一次性 exec（AGENTD_ARG_*）或 persistent JSON-RPC
配方：~/.data/xaihi/tasks.d/*.json（kind/steps[{run|spawn}]/restore/artifacts），未注册的 kind 不可派发
```

---

## 6. 人物卡

> 给下一轮的我：这个人怎么工作、喜欢什么、别踩什么。

### 6.1 用户

```
身份/场景：本机（Arch Linux）重度使用者，xaihi 是他替代 Open WebUI 的自用控制台；
          经常在**竖屏/窄窗口**里用（这一条极其重要），也常在宽屏多列里校对布局。
语言：中文说，中文看；厌恶"看起来差不多"的答复，要证据（数字、截图、命令输出）。

审美（硬约束）：
  · 冷灰青 + 纸感 + 少量机械动效（Endfield 风格）；只有 7 级灰 + 白，**不要黄色**
  · 方角、细线、宽体小字标签；卡片层级只变位置/内容，不变样式
  · 禁止 !important；强调只用底色/描边，不改文字色
  · 同一层级/同一语义必须同一套容器与样式（他明确说过"完全不可能出现不一致的布局"）

工作方式：
  · 先给结论与证据，再给细节；不要长篇解释过程，但关键判断要写清根因
  · 每完成一个部分要做一次前端检查（他已多次强调），不接受"构建过了就算完成"
  · 大改前先备份；文档随做随写（他要求把坑写进文档，避免下次重踩）
  · 说"继续"就是要你按计划一路做完，不要中途反复请示；但也别把没验证的东西当完成
  · 会压缩会话，所以交接文档必须能独立读（本文件就是）

雷区：
  · 半成品/白屏/点击失灵（他会立刻发现）
  · 同一功能两套实现各写一份样式（上两次都因此返工）
  · 假完成、只跑构建不跑用例、把失败说成"环境问题"
  · 明文回显密钥；手工改 .kbx；递归扫 $HOME
```

### 6.2 我（下一轮接手者）的原则

```
1. 先查证再动手：kb search → 仓库源码/docs → 互联网（带 7897 代理）；不确定就问一句，不猜。
2. 改配置先备份；长任务丢 systemd-run --user；清进程用 pgrep -x，不用 pkill -f。
3. 每块改动：npm run build → 跑相关套件 → 截图/量几何 → 再提交（提交信息写清根因与实测）。
4. 结构优先于补丁：能用"一套容器/一套状态"解决的，不写两套 if。
5. 视觉改动必须同时看宽屏（1600×1000）和竖屏（900×1000），并量 rect（x/y/w/h）而不是靠眼看。
6. 发现无关但真实的问题：先记下来，问一句或放到本文件 §7 的候选里，不顺手改乱。
```

---

## 7. 全面优化候选清单（下一轮做，按我建议的优先级）

> 优先级 P0 = 体验硬伤/低风险高收益；P1 = 明显提升；P2 = 锦上添花。
> 规模 S ≤ 半天、M ≈ 1 天、L 需要拆几步。**每一块都要能单独回滚**（tag/commit）。

### A. 视觉与信息层级

```
A1 P0 S  用户消息与 Agent 消息区分：用户消息换淡底/右对齐/去掉头像圆标；长会话里现在要靠读内容才知道谁说的
A2 P0 M  密度三档（data-ef-density: compact/standard/roomy）：把 1172 行 CSS 里的字号/间距收敛成一组变量
A3 P1 S  工具组头信息增强：总耗时、失败标记（exit≠0 / error）、"仅看失败"过滤
A4 P1 S  会话列表信息密度：标题两行截断、固定宽度的编号/时间列、hover 显示第一条摘要
A5 P1 M  状态栏与右栏指标去重：合成一条紧凑指标条（● 状态 · 耗时 · tok/s · 上下文%）
A6 P2 S  空状态加"最近 3 条会话"快捷入口，减少一次点击
```

### B. 交互与状态

```
B1 P0 M  跨会话运行态：抽屉里给正在跑的会话加小圆点/进度，切走再回来能看出"它还在跑"
B2 P0 S  回到底部浮标：离底 >400px 时右下角出现「↓ 回到底部」，并带新消息计数
B3 P0 S  Esc 逐级关闭：设置 → 抽屉 → peek（setOpenRef 已在，缺 keydown）
B4 P1 M  出错重试：error/中止后在消息尾部给「重试本条」，不用手动复制粘贴
B5 P1 S  工具组：正在执行的组自动展开、完成后自动收拢（现在默认全收拢，长任务看不到进度）
B6 P1 M  ↑↓ / J K 在消息间跳转（配合右侧 peek 与阅读位置），空格/Shift+空格翻页
B7 P2 S  复制增强：整条复制已有；加「复制为引用（No.012 + 前 80 字）」
B8 P2 M  截图 lightbox：点击放大、左右切换、Esc 关闭（现在只有缩略图）
```

### C. 性能

```
C1 P1 L  长会话窗口化（>300 条）：只渲染可视区 ±20 条；难点是工具组跨行与 data-vidx 阅读位置，
         需要先把消息列表拉平成"块"（每块 = 一条消息或一个工具组），已验证数据：575 条消息/174 组
C2 P1 S  列表 key 从 index 改成 seq（App 的 msgs.map 目前 key={i}），减少插入/重载时的重挂载
C3 P1 S  toolMeta 计算改一遍线性扫描（现在每行 runStart 会回退，组很大时是 O(k²)）
C4 P1 M  设置面板 memo 化：每次输入都重渲染五张卡；把四张卡各自 React.memo + 稳定回调
C5 P2 M  peek / readingIdx 从 App 状态里拆出去（现在滚一下就整棵树重渲染）
C6 P2 S  图片懒加载 + 缩略图（loading="lazy"；截图按 640px 生成缩略，点开看原图）
```

### D. 可访问性与一致性

```
D1 P0 S  所有可点击 div 改成 button 或补 role/tabIndex/Enter/Space（.item、.tn-hd、.ef-record…）
D2 P0 S  统一 focus-visible 焦点环（输入框/按钮/列表项），现在只有 .icon-x 有
D3 P1 S  对比度检查：--ink3(c4) on --paper(c1) 做正文小字时偏低，必要时换 c5
D4 P1 S  状态栏 aria-live="polite"（生成中/完成/已停止 播报），按钮补 aria-pressed
D5 P2 S  设置面板各卡片加 aria-labelledby，标题行按钮补 title
```

### E. 竖屏 / 窄屏（用户主场景，权重高）

```
E1 P0 M  键盘弹出时输入区吸附：visualViewport 监听，避免 composer 被遮住
E2 P0 S  竖屏点会话后抽屉收起已有；补"从正文左缘右滑打开抽屉"手势
E3 P1 M  竖屏工具组展开后自动滚到组头（现在展开可能停在中间）
E4 P1 S  竖屏状态栏压缩成一行（压缩中/生成中 + 耗时），给正文多留一行
E5 P2 S  横竖屏切换时保持滚动位置与展开态
```

### F. 设置面板

```
F1 P1 S  未保存就关面板：现在只有"编辑器"层 confirm；常规配置/模型表单直接丢改动 → 统一 closeOne 询问
F2 P1 M  工具/模型多了以后加搜索框与"只看启用中"过滤
F3 P1 M  模型表单分组（连接 / 生成 / 上下文 / 工具）加折叠，现在一屏 12 个字段
F4 P2 S  工具说明编辑加「从脚本 --help 生成默认说明」按钮
F5 P2 S  设置面板支持 ?tab= 深链定位到某一段（工具/模型/任务）
```

### G. 代码结构（下一步重构）

```
G1 P1 L  App.jsx 1096 行再拆：drawer / rail / composer / thread 四个区块各一个组件；
         把"消息加载 + 流 + 发送"收进 conversation/useConversation.js（约 500 行逻辑）
G2 P1 M  theme.css 1172 行按模块拆成 base / layout / messages / settings / responsive，
         vite 里 @import 合并（注意保持顺序）
G3 P2 S  给 messageGroups.js 补单测（纯函数，node --test 即可，无需浏览器）
G4 P2 S  抽出 <Button>/<Field>/<Switch> 三个基础件，替掉 15 处重复的 className 组合
```

**我的建议**：下一轮先做 **B2 + B3 + A1 + D1/D2**（一天内、低风险、用户能立刻感觉到），再做 **B1 + C4 → C1**（结构性收益最大），最后 A2/A5 统一视觉密度。每做完一组跑一遍全套用例。

---

## 8. 验证工具与方法

```
geckodriver：~/.local/bin/geckodriver --port 4444 &   （/tmp 会被清空，不要再放 /tmp）
套件（scripts/ui-test/，共 8 个）：
  run.py            宽/窄屏排版、设置三级卡、输入框、任务
  run2.py           JS 错误收集、任务点击、滚动、输入框间距
  run3.py           任务深链、竖屏布局、把手、抽屉
  check_layout.py   设置 2 级/3 级 容器一致性（模型=工具，宽窄屏量 rect）
  check_settings.py 脏检查/保存/取消/深链/localStorage 脏数据不白屏
  check_sendstop.py 发送/中止 按钮序列（MutationObserver 记录 class+disabled）
  check_tools.py    工具组结构、memo 命中（window.__renders）
  check_live.py     真跑一轮（实时气泡不残留、历史卡不重渲染）；跑完自动归档自造会话
跑法：cd ~/Workspace/xaihi && python3 scripts/ui-test/<name>.py
     每个套件之间要重启 geckodriver（Firefox 单会话）
截图：/tmp/uitest/*.png（脚本自己写；目录不存在会失败，先 mkdir -p）
后端：go test ./...
性能：window.__renders 差值；window.__stream 看缓冲；500 条会话用 conv 51
新增用例：复制 wd.py 的 Session 用法，断言尽量"量数字"（rect、count、序列），少用"看起来对"
```

---

## 9. 已知坑（改之前必读）

```
1  !important 会压过内联样式；本项目禁止用
2  ASI：`const q = new URLSearchParams(...)` 下一行以 `(` 开头（async IIFE）会被当成函数调用，
   曾导致整页白屏；这类地方前面留分号
3  localStorage 是用户可改的：ui.quips 存成 "null" 时 JSON.parse 返回 null，
   旧代码直接读 length → effect 抛错 → React 卸载整棵树（白屏）。读之前 Array.isArray
4  streamStore 的 80ms 节流 push 与 clear 有竞态：收流/切会话要 clearTimeout 再 clear，
   否则会出现"正文已落库、底下多一个生成中…"的幽灵气泡
5  中止必须用 ref（abortedRef）判断，用 state 在闭包里永远是旧值 → 会被当成断流去重连，
   状态最后变成"已同步"而不是"已停止"
6  设置面板的 is-top 只能由 Panel 按层级算（3 级 > 2 级 > 1 级），每张卡自己判断会出现
   两张卡同时 is-top → 窄屏两张卡一起显示
7  2 级卡只能有一个类名 col-detail（模型/工具同一张卡）；不要给某类卡单独写容器或栅格样式
8  openRuns（工具组展开态）用组下标当 key，换会话必须清空
9  工具组编号/统计依赖 messageGroups.js 的纯函数；改渲染前先看它们的语义（ghost 跳过）
10 空会话没有正文列：guideline/col-resize/startColDrag 都要按 msgs.length 门控
11 改前端只需 npm run build（xaihi 直接读 dist），不要为此重启服务
12 /tmp 会被清空：geckodriver 放 ~/.local/bin，测试脚本放仓库，截图目录先建
```

---

## 10. 下一轮开工清单

```
[ ] git log --oneline -5 ｜ git tag | tail 3（确认在 5c2a9dc 或之后）
[ ] systemctl --user is-active xaihi xaihi-web-dev；curl /api/health
[ ] mkdir -p /tmp/uitest；起 geckodriver；跑 8 个套件确认基线全绿
[ ] 读本文件 §7，选主题（建议 P0：B2 回底浮标 / B3 Esc / A1 用户消息区分 / D1-D2 可访问性）
[ ] 每块：改 → build → 跑相关套件 → 宽窄屏截图/量 rect → commit（信息写根因+实测）
[ ] 每完成一组回填本文件 §7（在条目前标 ✅ 与实测数字）
[ ] 收尾：更新 docs/UI.md（视觉规则变化）、REFACTOR-UI.md（结构变化）、本文件状态行
```

---

## 11. 文档索引

```
docs/README.md          文档索引
docs/API.md             接口细节（本文件 §5 是速查版）
docs/DESIGN.md          后端设计（工具/上下文/流式/设置分层/云访问）
docs/UI.md              视觉与交互约定（§13 架构、§12.2 发送中止、§12.1 空状态）
docs/REFACTOR-UI.md     前端重构三步 + 坑位清单
docs/HANDOFF.md         交接/唤醒功能
docs/PLAN.md            待办与想法池
docs/REPAIR.md          故障排查
docs/UI-OPTIMIZATION.md 本文件（下一轮的交接包）
```
