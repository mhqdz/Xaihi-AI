# agentd 前端视觉约定

页面观感 = 冷灰青 + 纸感 + 少量机械动效（endfield 风格）。以下规则优先级高于个人偏好，
新增样式只能落进对应小节，禁止用 `!important` 压规则。

## 1. 颜色：只有 7 级色阶 + 白
```css
--c1:#F3F5F5  --c2:#DEE4E5  --c3:#C4CECF  --c4:#99AAAD
--c5:#778587  --c6:#596364  --c7:#3A4142        --panel:#fff
--c8:#637172  （比 c7 稍浅，用于小字号辅助文字，如 .no / .tt）
```
语义映射（全部指向色阶，不要另造颜色）：

| 变量 | 值 | 用途 |
|---|---|---|
| `--paper` | c1 | 页面底 |
| `--line` | c3 | 分隔线、卡片描边 |
| `--ink` / `--ink2` / `--ink3` | c7 / c5 / c4 | 正文 / 次要 / 弱化 |
| `--theme` | c4 | 数字块底、编号底、发送按钮 |
| `--side*` | c7/c6/c1/c5 | 深色块用途（数字块文字等）；折叠把手的悬停色是 c4，不走这组 |

规则：强调只用"底色/描边"，不改文字色去强调；一屏出现的强调色种类不超过 2。

## 2. 字体：Maple Mono NF CN（内置）
```
web/public/fonts/  MapleMono-Regular.woff2 / -Bold.woff2（官方拉丁）
                   MapleMono-CN-subset.woff2（GBK 子集）
@font-face 用 unicode-range 分工；endfield 的 --ef-font-* 也被指向它
body: 14px/1.7 "Maple Mono NF CN", ui-monospace, …
```
新增字号只在 4 档内取：`.62rem`（标签/宽体小字）、`.72rem`（次要）、`.86rem`（正文）、`1.45rem`（数字块）。

## 3. 卡片：一套结构，多处复用
```
.card       width:var(--card-w,400px); max-height:100%; 白底 1px 描边；无 !important
.card-hd    标题行（宽体小字 + 下划线），右侧放动作按钮
.card-bd    唯一滚动容器（内容超高时只滚这里）
.item      一行配置项：.item-title（可点，600）/ .item-desc（3 行限高，不可选中）
.item.static 只读项（不加粗、不随 hover 变色）
.field      表单字段：.field-label + 方角输入
.choices/.choice  多选组
.btn        切角按钮（右上切角＝可点击）
```
层级差异只体现在"位置与内容"，不体现在样式。

### 配置面板的三层语义（2026-11 审计后固定下来）

| 层级 | 卡片组件 | 放什么 | 判据 |
|---|---|---|---|
| 1 级 `col-list` | `ListCard` | 通用配置 + 详细配置入口 | 只放"全局开关/数字"和"点进去"的行 |
| 2 级 `col-detail` | `ModelForm` / `ToolCard` | 详细配置 | 一组字段，按 `Section` 分组 |
| 3 级 `col-editor` | `EditorCard` | 长文本输入 | 铺满、无边框、只放一个文本域 |

2 级的三张卡（模型详情 / 工具详情 / 插件表单）共用 `components/Settings/form.jsx`：
`Section` / `Field` / `InfoField` / `SwitchRow` / `LinkRow` / `ChoiceGrid`。
前端不解释字段语义（哪个是地址、哪个是路径），只负责"标签 + 控件 + 提示"这一层排版，
所以内置工具、外置插件、模型配置天然是同一套样式。

**两处"长文本放在 2 级"的例外，以及理由**：

- 插件「命令」和「JSON Schema properties」：它们是有结构的（argv 列表 / schema 对象），
  不是自由文本；而且改它们的时候通常要同时改名称和超时，拆到 3 级反而要来回跳。
- 一级卡的「运行中提示」：存在本机 `localStorage`，是本机偏好而不是后端配置，
  跟着"常规配置"走比单开一张 3 级卡更顺。

**排版基线**（`check_config.py` 会量）：字段标签 14px、小节标题 10.88px、
输入框 35px 高；字段间距 11px、分组间距 50px。

## 4. 布局
```
左：抽屉（默认 250，右缘 6px 透明热区可拖 200–520）
    顶部三格数字（会话/归档/本轮）→ 会话列表 → 空白区（＋新会话）→ 底条 WORKSPACE
中：消息流（卡片 + 工具记录），顶部 2px 全局进度条 = 阅读位置 / 可见轮次
右：竖排标题 + 轮次数字 + 状态 + ＋新会话 + 设置三点（≥1400px 才显示）
```
隐藏：会话区滚动条；页面无顶栏（原顶栏已移除）。

## 5. 动效
- 时长：0.12–0.3s；缓动 `cubic-bezier(.2,.8,.2,1)`；只用 transform/opacity/background。
- 已有：数字翻动（number-flow）、数字块 hover 高亮滑入、折叠把手图标旋转、
  ＋ 悬停旋转 22.5°、卡片头部按钮按需出现。
- 不做：弹跳、缩放强调、超过 0.4s 的过渡。

## 6. 文案
标签只写必要约定（如 `/v1`），常识性说明一律删（如"勾选启用""本地或云端"）。
按钮动词优先：保存 / 取消 / 关闭 / 删除 / 恢复。

## 7. 已踩过的坑
```
· !important 会压过内联样式 → 曾导致 JS 设的 height 失效
· grid-template-areas 与 grid-template-columns 列数不一致 → 整条声明被浏览器丢弃（查不到该属性）
· 给祖先加 z-index 会形成层叠上下文 → 面板被遮罩盖住，点击失效
· .ef-read / .ef-prose 自带字体变量 → 与全局字体不一致；已在 theme.css 里把
  --ef-font-ui / --ef-font-text / --ef-font-mono 三条覆盖成 Maple Mono NF CN，
  盖掉 vendor 的 Saira（新增字体时记得这三条要一起改）
```

## 8. 消息的三种形态

```
对话（用户/Agent）：白底卡片 + 顶部实线 + 头像 + 名称 + 编号
工具（bash 等）    ：一个"工具组"（.toolnote）：组头 Tool ▸/▾ + 编号区间 + 思考段数 + 条数 + 字数，
                   默认收拢；展开后每行 = 编号 / 工具名 / 耗时 / 字数 + 参数 + 该工具的思考 + 输出折叠
系统提示           ：无卡片；左上/右下角标 + 极浅底 + 尾部细线 + 轻量编号 [No.xxx]
```
- 工具结果**不并入** Agent 气泡（有自己的 seq，读者可按编号引用）
- 系统级提示（工具轮次上限 / 已停止 / 中断 / 空转）识别规则见 `isSysNote()`；
  它们入库（模型要看到"为什么停"），但用系统样式渲染
- 编号：库里的 seq；流式中还没落库的消息用 `maxSeq+序号` 临时编号，避免撞号

## 9. 思考过程

```
折叠态：▸ 思考过程 · N 字 · Xs        （details/summary）
展开态：正文（无高度上限、无额外边框，只有一条左侧竖线）
        底部「⌃ 收起思考」条：细线 + 小箭头，灰阶；点它收起并回到该条消息开头
```

## 10. 右栏

```
宽屏 64px 竖栏：AGENT EXECUTE / 本轮轮次 / ＋新会话 / ⋮⋮设置
                底部一组：● 运行中 → 速度 tok/s → 上下文百分比
窄屏 <1400px：整条右栏水平搬到顶部（进度条上方），正文占满宽度；
              左栏变成浮层（带遮罩），设置面板变成单列层叠
上下文百分比：常态只显比例，悬停看 "X / Yk tokens（已压缩） · 原始 Zk"；
              达到 compact_pct 时变深加粗（不用黄色）
```

## 11. 按钮与标记

```
.card .btn    直角 + 1px 边框 + 左上/右下角标；悬停加深边框与底色
.btn.danger   删除类（删除配置 / 删除插件），只靠灰阶加深区分
.icon-x.back  22×22 角标热区 + 细线 ×；悬停显边框
.src-tag      工具来源徽标，仅内置显示
切角（clip-path）/ 渐变虚化线：已移除（除会话栏那条阅读辅助线外不再用）

### 竖屏输入框

```
默认 52px（宽屏 92px）；切到竖屏时把过高（>52）的收回来
右下角拖拽仍可调高；多行内容在框内滚动
composer 整体高度 ≈ 133px（原 173），给正文多留 40px
```

### 抽屉与竖屏交互

```
· 抽屉底部「设置」：单行、不换行、深色文字（曾经被挤成两行且接近白色，看起来像坏掉的方块）
· 竖屏选完会话 → 抽屉自动收起（浮层不该遮住正文）；宽屏保持展开
· 竖屏把手在顶栏最左：chevron 朝右=可展开，抽屉打开时朝左=可收起
```

### 子会话（handoff）

```
抽屉：父会话下面紧跟子会话，缩进 + 左侧竖线引导；第一行用 #N（子序号），时间位置改成真实会话编号 No.xxxx
      （父会话仍是 No.xxxx + 最后活跃时间）；没有父会话时按顶层显示
打开：顶部一条 sticky 归属条「#N 子会话 · 父 No.0084」，点父编号跳回
任务视图：详情里「打开子会话 No.xxxx」直接跳转
标题：用交接卡里的 goal 截断（不带箭头）；拿不到才退回「kind · 父 No.xxx」
```

### 滚动跟随

```
在底部（距底 ≤40px）时：新内容到达（含流式增量）→ 正文向上翻滚，视口始终贴着底部
不在底部时：内容照常增长，但视图完全不动（用户读到哪就是哪）
运行中额外用 requestAnimationFrame 每帧校正（流式增量不保证触发 React effect）
收尾（finish）时也不回底——除非当时就在底部；发送消息 / 打开会话仍会主动回底
先滚到底再发长输出实测：高度 695→2123 px，距底始终 0；上翻 900px 后继续增长 +8829px，scrollTop 不变
```

### 工具轮与"执行中"

```
工具轮的 assistant 消息正文是空的（只有思考和 tool_calls）→ 不再渲染成空 Agent 卡，
改成上方一行「▸ 思考 · N 字」（可展开），下面直接是工具卡
工具开始执行时（tool_start）在流末尾插入一张「执行中… 0.0s」卡：工具名 + 参数 + 计时
  → 长工具（bash/搜索动辄 10~40s）期间不再是白屏，也能解释"GPU 空闲"其实在等外部命令
结束/中断时清空所有执行中卡
```

### 工具输出与工具组

```
工具组：连续的工具消息合并成一个组，组头只在第一行渲染（Tool ▸/▾、编号区间、思考段数、条数、字数）
        默认收拢；组内每行 = No.xxx + 工具名 + 耗时 + 字数 + 参数 JSON（UTF-8 还原）
        展开后每行下面可展开「思考」与该行「输出」；四角括号标出组的首尾
空正文 assistant：工具轮的 assistant 正文是空的，不渲染成 Agent 卡（isGhostMsg），
                 它的思考并进下一行的「思考」折叠里
分组纯函数：components/../conversation/messageGroups.js（isGhostMsg/runStart/runStats/thinkBefore）
```

## 12. 自动化检查（排版 + 交互）

```
scripts/ui-test/   WebDriver（geckodriver，无 selenium）驱动 Firefox headless
断言：无横向溢出 / 右栏 64px / 设置三级卡显隐与互斥 / 卡片同宽 / 思考展开无内滚 + 收起回滚 /
      任务展开与深链滚动 / 打开会话在底部 / 最后一条消息不被输入框遮挡 / JS 无错误
截图：/tmp/uitest/*.png（宽屏三卡、工具表单、任务详情、窄屏单卡、编辑卡）
```

## 12.2 发送 / 中止的过场（按钮转圈）

```
发送键四态：send（可发）→ send loading（转圈，disabled）→ send stop/interject（生成中）→ 回到 send
  · 点发送/插话：立刻 sending=true，按钮变 .send.loading（disabled），后端开始响应（fetch 拿到响应头）才解除
  · 点中止：立刻 stopping=true，按钮变 .send.loading（disabled），busy 落定才解除；15s 兜底防后端卡死
  · Enter 键同样受 sending/stopping 门控（过场期间不接受新动作）
  · spinner 是方框旋转（Endfield 方角），白圈 + 亮边，:disabled 不降透明度（否则看不清）

顺带修的真 bug：中止后状态一直显示"已同步"而不是"已停止"
  原因：sendText 的 catch 里 `if (!aborted && cur)` 读的是 state（闭包里永远是旧值 false），
  于是"用户中止"被当成"断流"→ 又 attachStream 重连 → 收尾状态被覆盖成已同步。
  改成 abortedRef（useRef）后：中止直接走 AbortError 分支，状态 = 已停止，且不再重连。
  回归用例 scripts/ui-test/check_sendstop.py（用 MutationObserver 记录按钮 class/disabled 的完整序列）
```

## 12.1 空状态（新建会话页）

```
新建会话页没有正文列可拖：不渲染 .guideline、不显示 col-resize 光标、禁止 startColDrag。
（.empty 的内容宽度是固定的 44rem，列宽 colW 对它无效 —— 留着只会是一条"看着像边框、拖了没反应"的线）
有消息时才出现可拖动的列宽线。
```

## 13. 前端架构（2026-10-30 定：方案 A）

```
数据层（无 JSX）
  conversation/streamStore.js   流式缓冲，React 之外；useSyncExternalStore 订阅
  conversation/useConversation  会话 reducer + hooks（消息/上下文/任务）
展示层（纯组件 + memo，props 全是父层算好的结果）
  components/Markdown.jsx       Md / Sections（marked + DOMPurify）
  components/MessageCard.jsx    已完成消息（memo：m / seq / vidx / onCollapse）
  components/ToolGroup.jsx      工具组（默认收拢，memo：逐字段比 meta）
  components/SysNote.jsx        系统提示
  components/Settings/*.jsx     设置卡片（Card 外壳 + Panel/ListCard/ModelForm/ToolCard/EditorCard）
                                2 级卡统一 col-detail、3 级 col-editor，层级由 Panel 决定（is-top 只有一张）
  settings/useSettings.js       设置状态机（保存/取消/脏检查/深链/插件增删改）
App.jsx                       只做布局、URL、快捷键、装配
```

迁移步骤（每步可独立回滚，tag：backup-web-20261030）：

```
Step 1  ✅ 流式缓冲移出 React state（streamStore + LiveBubble），历史消息不再随 delta 重算
Step 2  ✅ 消息卡抽组件 + React.memo（实测：204 条会话里切换工具组，MessageCard 渲染 0 次）
Step 3  ✅ App 只留装配（1067 行）；设置面板拆成 5 张卡 + useSettings 容器
验收：流式中每秒渲染 ≤10、只有尾部与状态栏重渲染；500 条会话滚动/新增不掉帧
```
