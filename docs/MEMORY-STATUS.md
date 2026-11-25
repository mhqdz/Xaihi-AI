# 持久化 / 记忆系统：现状与缺口

写于 2026-11-19。用途：给设计讨论提供事实基础（对照 Anthropic 那篇
《The components of agent memory》，https://www.anthropic.com/research/components-of-agent-memory）。

**这份文档只描述现状，不含设计方案。** 所有路径、常量、行号都对着当前代码核过。

---

## 一、已有的四层，和论文对得上

论文把 agent 记忆分成四层：transcripts → memory notes → representations → injected context。
我们这四层都有，只是内容和论文里的形态不完全一样。

| 论文层 | 我们的实现 | 位置 |
|---|---|---|
| transcripts（转录） | sqlite 消息表，按 conv_id + seq 有序 | `~/.data/xaihi/xaihi.db` |
| memory notes（记忆） | front-matter + 正文的 md 文件 | `~/.data/xaihi/memory/<scope>/*.md` |
| representations（表示层） | kb（分类树 + `.kbx`） | `~/.data/.kb` |
| injected context（每轮注入） | 人格 → 时钟 → 日记提示 → 待办 → 记忆 → 摘要 | `internal/agent/loop.go:250-271` |

注入顺序在代码注释里写明了意图：

> 顺序：人格 → 时间感 → 记忆 → 已知事实（越易变越靠后，KV cache 才能多命中）

---

## 二、各层的实际参数

### 2.1 转录（transcripts）

- 存储：`internal/store/sqlite.go`，`messages` 表
- 一个会话的体量参考：conv 1469 有 **4647 条消息**、约 280 万字符
- 检索：`recall` 工具，**只搜当前会话**（`internal/tools/tools.go`），
  默认关键词 OR 匹配，`re=1` 走 RE2，单次最多返回 3 条
- `Save()` 是**只追加**语义（`m.Seq != 0 { continue }`），历史不会被改写

### 2.2 记忆（memory notes）

代码：`internal/memory/memory.go`

| 项 | 值 |
|---|---|
| 单条正文上限 | `MaxBodyRunes = 2000` |
| 状态机 | `active` / `superseded` / `archived`（软删除，可 restore） |
| 注入预算 | `BudgetFor(contextWindow)`，默认 **6000 字符** |
| 选取策略 | 从新到旧装，装不下的跳过并继续试更短的 |
| 现有条目 | `memory/global/` 下 **10 条** |

写入靠 agent 主动调 `memory_write`；`forget` / `supersede` / `restore` / `purge` 分别对应
归档、被取代、恢复、彻底删除。

### 2.3 表示层（kb）

- 独立 CLI `kb`，存储 `~/.data/.kb`，条目是 `.kbx`（**禁止手工编辑**，只能由 kb 生成）
- 现有 **33 条**，按分类树组织
- **不注入**，靠 `kb search` / `kb get` 按需读取

### 2.4 注入层（injected context）

代码：`internal/agent/loop.go:250-271`，拼装顺序：

```
profile.system（人格）
  → clockText（时间感）
  → journalHint（日记在哪、怎么读怎么写）
  → todoText（待办）
  → memText（记忆，预算内）
  → [user] [更早对话的摘要]（c.Summary）
```

体量估算（当前安装版人格）：

```
人格 14,865 B + 记忆 ~6000 + 待办 + 时钟 + 日记提示 + 摘要 ≈ 21 KB
```

### 2.5 摘要（compaction 之外的另一种）

`maybeSummarize`（loop.go:731）：

- 只在总量超过 `compactAt()` 时触发
- 取 `c.Messages[:cut]` 交给模型写摘要，存进 `c.Summary`
- 注入时作为一条 user 消息 `[更早对话的摘要]`

**注意**：它和 `compact`（surface 裁剪）是两套机制。`compact` 只影响这一轮发给模型的副本，
不动数据；`summary` 是持久化到会话字段的。

### 2.6 日记（journal）

代码：`internal/journal`，数据 `~/.data/xaihi/journal/`

- **不注入**。只通过 `journalHint()` 告诉模型"有日记、按日期读、写用 journal_write"
- 按日期一个 md 文件，现有 4 个（11-10 / 11-14 / 11-17 / 11-18）
- 定位（代码注释原文）："叙事层——她过过的日子"

### 2.7 待办（todo）

代码：`internal/todo`，注入。语义是"您欠着的事"（`Add(what, at, convID)`）。

---

## 三、和论文结构相比，缺的五件事

### ① 没有 dreaming（离线整理）

论文里 dreaming 的作用：回看转录 → 对照记忆 → 发现记错或过期 → 改写。

典型例子：Pokémon 里记忆把 PokéCenter 记在了东侧（实际在西侧），
dreaming 对照转录发现这个错并改写，之后才走出山洞；不改的话它反复掉回原地。

**我们现在完全没有这一环。** 记忆只增不改，除非人手动编辑文件。

### ② 没有"判断什么值得记"的约束或引导

`memory_write` 是 agent 随手调，写什么全看当场判断，没有任何反馈。

论文的对照数据值得注意：

| 模型 | 记忆文件数 | 结果 |
|---|---|---|
| Sonnet 3.7 | 413 个文件 / 225 个目录 | 只拿到 1 个徽章，大量时间花在翻记忆上 |
| Fable 5 | 9 个文件 + 1 个 `global.md` | 拿到 4 个徽章 |

结论是"判断重要性"本身是核心能力，而我们现在既没有约束也没有引导。

### ③ 转录检索只覆盖当前会话

`recall` 只搜 `conv_id` 匹配的消息。跨会话的"上周那个问题怎么解的"没有入口。

### ④ kb 和 memory 是两套不相通的东西

| | kb | memory |
|---|---|---|
| 谁写 | 人（或人审过的 agent 草稿） | agent 自己 |
| 内容 | 机器/环境事实 | 她自己的笔记 |
| 形态 | 分类树 + 链接到 id | 平铺 + 状态机 |
| 注入 | 否，按需读 | 是 |

论文里的 representations 层（wiki）是 **agent 自己建立、自己维护**的表示层，
用来做"跨文档的索引和图"。我们这一层目前是"人的知识库"，不是 agent 的表示层。

两者之间没有链接、没有合并、没有 promote 通路。

### ⑤ `diary/` 是死层

`~/.data/xaihi/diary/` 只有一个 `2026-11-10.md`，
`grep '"diary"' --include=*.go internal/` **零命中**。

应该是早期设计残留，被 `journal` 取代后没清理。建议确认后删除或合并。

---

## 四、一个值得注意的约束

论文提到 Claude Code 的自动记忆只加载 `MEMORY.md` 的**前 200 行 / 25KB** 当索引用，
并明确说：

> longer instruction files reduce adherence

我们现在注入层约 21 KB，已经接近那个量级。而人格卡是"档案体"（人物设定 + 叙事），
不是"索引体"——一旦继续加内容，遵循度会往下掉，而且人格卡本身也会被稀释。

**这是设计时要先决定的事**：人格卡和操作指令要不要拆成两段、各自注入。

---

## 五、可选方向（供讨论，未定）

| 方向 | 改动量 | 预期收益 |
|---|---|---|
| 加 dreaming（每天读昨日转录 → 校对/改写记忆 → 必要时 promote 到注入层） | 中 | 最大，直接补上"记忆会腐坏"这个洞 |
| `recall` 扩到跨会话（会话 + 日记 + 记忆一起搜） | 小 | 中，取决于记忆质量 |
| 让 agent 自己建表示层（kb 从"人写"变成"agent 写 + 人审"） | 大 | 大，但风险也大（需要审校成本） |
| 清理 `diary/` 死层 | 极小 | 无功能收益，只是不再误导 |

---

## 六、文件索引

| 主题 | 路径 |
|---|---|
| 提示词拼装 | `internal/agent/loop.go:250-271` |
| 摘要 | `internal/agent/loop.go:731` |
| surface 裁剪 | `internal/agent/loop.go`（`compact` / `buildSurface`） |
| 记忆 | `internal/memory/memory.go` |
| 日记 | `internal/journal/journal.go` |
| 待办 | `internal/todo/todo.go` |
| 转录存储 | `internal/store/sqlite.go` |
| 检索工具 | `internal/tools/tools.go`（`recall`） |
| 人格卡 | `~/.data/xaihi/personas/xaihi.md`（14,865 B，已安装） |
| 人格草稿 | `docs/persona/`（`xaihi.md` 3,360 B / `lore.md` / `lexicon.md` / `README.md`） |
