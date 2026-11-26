# 持久化 / 记忆系统：现状、边界与缺口

写于 2026-11-19。用途：给设计讨论提供事实基础（对照 Anthropic 那篇
《The components of agent memory》，https://www.anthropic.com/research/components-of-agent-memory）。

本次更新：修正工具名、预算单位和过时描述；记录已确认的 memory / wiki / kb 边界；
P0（可靠存储）、P1（证据化、可替换记忆）及人格 profile 隔离结构已实现。
wiki 已有独立存储和 CRUD / 搜索工具；手动、单人格、wiki-only dreaming 已实现。
memory apply、定时 dreaming 与同人格跨会话 recall 尚未实现，详见 `docs/DREAMING.md`。

---

## 〇、已确认的三类存储边界

| 存储 | 定位 | 谁写 / 谁改 | 坏了怎么办 |
|---|---|---|---|
| **memory** | agent 自己的笔记：偏好、约定、关系、经历、验证过的经验 | agent 可写；新内容可腐坏，需要凭 transcripts 做 dreaming 校对 | 创建新版本并 supersede 旧版本，保留证据链 |
| **wiki** | agent 自己建立的派生表示 / 索引层 | agent 可以通过 wiki_* 整理、合并、重构 | 可以从 transcripts 和 memory 重建，不当作事实源头 |
| **kb** | 人审过的外部事实与环境知识 | 人审后由 kb 写入；agent 只读 | 由人的审校流程修正 |

wiki 独立于 kb，可以通过 kb id 建立链接，但不能借 dreaming 自动改写 kb。
memory、wiki、kb 不再互相兼任：memory 是经验笔记，wiki 是可重建的地图，kb 是人审事实。

### 人格 profile 是 memory/wiki 的唯一 owner

会话 / 模型 profile 与人格 profile 是不同文件：

```text
profiles.json                      会话 / 模型配置，用 persona 引用人格
personas/<name>.md                人格正文
personas/<name>.json              人格配置：memory / wiki 开关
memory/personas/<name>/           该人格的 memory
wiki/personas/<name>/             该人格的 wiki
```

多个会话 profile 引用同一人格时共享 memory/wiki；不同人格之间不提供互通工具。kb 是唯一共有知识层。
人格 JSON 不存在时，memory 兼容旧会话 `persistent=true`；JSON 存在后以人格配置为准。wiki 默认关闭。
memory_* / wiki_* 属于“人格”工具组，空 `Tools` 表示无工具，运行时还会复核人格及对应能力。

旧共享 `memory/global` / `memory/project` 只在**唯一人格**时复制到其目录，原目录保留、同名文件不覆盖；
多个人格时不猜测旧记忆归属，不做自动复制。

---

## 一、论文的四层与当前实现

论文可以按四层理解：transcripts → memory notes → representations → injected context。
四层均已有对应结构；表示层支持工具写入和手动 dreaming 增量建页，尚无定时自动构建。
**kb 不算 agent 的 representations。**

| 论文层 | 我们的实现 | 位置 |
|---|---|---|
| transcripts（转录） | sqlite 消息表，按 conv_id + seq 有序 | `~/.data/xaihi/xaihi.db` |
| memory notes（记忆） | 每个人格独立的 front-matter + 正文 md 文件 | `~/.data/xaihi/memory/personas/<name>/<scope>/*.md` |
| representations（表示层） | 每个人格独立、可重建的 wiki | `~/.data/xaihi/wiki/personas/<name>/*.md` |
| injected context（每轮注入） | 人格 → 时钟 → 日记提示 → 待办 → 记忆 → 摘要 | `internal/agent/loop.go` |

注入顺序在代码注释里写明了意图：

> 顺序：人格 → 时间感 → 记忆 → 已知事实（越易变越靠后，KV cache 才能多命中）

当前 `active` 记忆仍全部参与注入筛选。“长期笔记”与“少量核心注入索引”的拆分属于 P4，
不在 P0 / P1 中提前改变运行行为。

---

## 二、各层的实际参数

### 2.1 转录（transcripts）

- 存储：`internal/store/sqlite.go`，`messages` 表。
- 一个会话的体量参考：conv 1469 有 **4647 条消息**、约 280 万字符。
- 检索：`recall` 工具，**只搜当前会话**（`internal/tools/tools.go`），
  默认关键词 OR 匹配，`re=1` 走 RE2，单次默认最多返回 3 条。
- 常规 `Save()` 只追加新消息；流式占位行由 `InsertPending` / `UpdateMessage` 单独更新状态与正文。

### 2.2 记忆（memory notes）

代码：`internal/memory/memory.go`。

| 项 | 当前实现 |
|---|---|
| 单条正文上限 | `MaxBodyRunes = 2000` |
| 状态机 | `active` / `superseded` / `archived`（软删除，可 restore） |
| 依据 `basis` | `explicit`（用户明确说）/ `verified`（有工具或转录证据）/ `inferred`（模型推断，默认） |
| 来源 `source` | 在线工具自动记录触发写入的 `conv:<id>#<seq>`；无 seq 的内部调用退化为 `conv:<id>` 或空 |
| 修订元数据 | `updated`、`superseded_by`、`supersedes` |
| 并发版本 | 每条记忆有原始文件字节的 SHA-256 `revision`，替换时做乐观检查 |
| 注入预算 | `BudgetFor(contextWindow)`，默认 / 封顶 **6000 个估算 token** |
| 选取策略 | 按 created 从新到旧装，装不下的跳过并继续试更短的；拼装再按 created 升序 |
| 现有条目 | 原文统计时 `memory/global/` 下 **10 条** |

`source` 自动证明“这条记忆在哪个工具调用中写入”，但不自动证明正文为真；`basis` 是写入时声明的
证据类型，也不是系统完成验证的保证。dreaming 必须沿 source 回查用户原话、工具观察等原始证据，
不能仅凭 assistant 自己当时的总结把 inferred 提升为 verified。

模型工具：

- `memory_save(body, kind, basis)`：新增记忆，自动填写 source；basis 不填就是 inferred。
- `memory_list()`：列出 active 记忆，含 source、basis、revision 和正文。
- `memory_supersede(name, revision, body, kind?, basis?)`：创建新版本，旧版本标 superseded，
  旧正文不覆盖，并建立双向替换链；revision 冲突必须重新读取。
- `memory_forget(name)`：标 archived，不进提示词但保留文件。

内部 API 另有 `Supersede` / `Restore` / `Purge`；真删仍不进模型工具。

可靠存储（P0）：

- 新文件采用“临时文件写完并 fsync → 原子 hard link”，已存在文件采用“临时文件 → 原子 rename”。
- 同一记忆根目录跨 Store 实例共享进程内互斥锁，并通过 `.lock` 的 `flock` 协调不同进程。
- `Replace` 必须携带最近读取的 revision；flock 不能约束人工编辑器，因此 revision 是 dreaming 的必要保护。
- 状态变更和替换保留未知 front-matter 字段，不再只写固定字段而丢元数据。
- 记忆名字拒绝路径穿越；id 扩大随机部分并采用无覆盖创建。

注意：两个文件的 Replace 有错误回滚和逐文件原子写，但不是跨文件崩溃事务；未来实现自动 dreaming
时仍应保留审计与恢复能力。

### 2.3 表示层（wiki）

代码：`internal/wiki/wiki.go`，数据：`wiki/personas/<name>/`。

- 单页是 Markdown + front matter（`name / updated / kb`），正文上限 20000 runes。
- `kb` 保存去重、排序后的 kb id 链接；不会写入或修改 kb，也不验证 id 是否仍存在。
- 工具：`wiki_list` / `wiki_search` / `wiki_read` / `wiki_write` / `wiki_delete`。
- 新建不带 revision；更新和删除必须带最近读取的 revision，冲突时拒绝覆盖。
- 搜索只接受 query / limit，服务端固定在当前人格 Store，没有跨人格参数。
- wiki 不注入 system prompt，也不参与 memory 的证据链；坏了可删除并从 transcripts / memory 重建。
- 已支持手动 dreaming 增量建页；没有定时运行、语义合并、断链修复或全量重建。

### 2.4 外部知识（kb，不是 agent wiki）

- 独立 CLI `kb`，存储 `~/.data/.kb`，条目是 `.kbx`（**禁止手工编辑**，只能由 kb 生成）。
- 原文统计时有 **33 条**，按分类树组织。
- **不注入**，靠 `kb search` / `kb get` 按需读取。
- agent 只读；wiki 可以链接 kb id，但 dreaming 不直接写 kb。

### 2.5 注入层（injected context）

代码：`internal/agent/loop.go`，拼装顺序：

```
profile.system（人格 + 任务指令）
  → clockText（时间感）
  → journalHint（日记在哪、怎么读怎么写）
  → todoText（待办）
  → memText（记忆，预算内）
  → [user] [更早对话的摘要]（c.Summary）
```

记忆渲染会标明 basis 对应的中文依据（用户明确 / 已验证 / 推断），旧数据没有 basis 时保持原样。

### 2.6 摘要（compaction 之外的另一种）

`maybeSummarize`（`internal/agent/loop.go`）：

- 只在总量超过 `compactAt()` 时触发。
- 取 `c.Messages[:cut]` 交给模型写摘要，存进 `c.Summary`。
- 注入时作为一条 user 消息 `[更早对话的摘要]`。
- 每条输入消息只取末尾 400 runes，不能把摘要当作完整原始证据；取证仍应回 transcripts。

**注意**：它和 `compact`（surface 裁剪）是两套机制。`compact` 只影响这一轮发给模型的副本，
不动数据；`summary` 是持久化到会话字段的。摘要也不等于 memory notes。

### 2.7 日记（journal）

代码：`internal/journal`，数据 `~/.data/xaihi/journal/`。

- **不注入**。只通过 `journalHint()` 告诉模型“有日记、按日期读、写用 journal_write”。
- 按日期一个 md 文件，原文统计时有 4 个（11-10 / 11-14 / 11-17 / 11-18）。
- 定位（代码注释原文）：“叙事层——她过过的日子”。

### 2.8 待办（todo）

代码：`internal/todo`，注入。语义是“您欠着的事”（`Add(what, at, convID)`）。

---

## 三、仍然缺的能力

### ① 已有手动 wiki-only dreaming，完整闭环仍未完成

论文里 dreaming 的作用包括：回看转录、对照记忆、纠错、合并、建立表示层，以及调整注入层级。

当前 `xaihi dream` 已能在显式范围内读取同一人格 transcripts，调用人格 dreamer 生成 DreamPlan，
经证据与 revision 校验后 dry-run 或只应用 wiki；审计不含完整转录，重跑相同内容为 noop。
`memory_actions` 仅输出，不应用。详见 `docs/DREAMING.md`。

仍未实现：memory apply、自动定时、增量检查点、大规模分块、全量 wiki 重建和前端 dreamer 错误展示。

### ② 有简单写入引导，但缺反馈与评测

`memory_save` 已说明记忆用途、粒度和 basis，并非“完全没有引导”。目前缺的是：

- 写入后是否真有帮助的反馈；
- 错误记忆能否被发现并修正；
- 重复记忆能否合并；
- 面向 Xaihi 场景的小型回归评测。

论文同时表明，对较强模型使用“只记失败”等硬规则可能降低收益。因此先做简单用途引导与校验闭环，
不做复杂的重要性评分或僵硬分类配额。

### ③ 转录检索只覆盖当前会话

`recall` 只搜当前 `conv_id` 匹配的消息。跨会话的“上周那个问题怎么解的”仍没有入口。
后续只允许同一人格 profile 内搜索，彻底不提供跨人格搜索；这是 P2，也是高质量 dreaming 读取证据的基础。

### ④ wiki 有基础结构，但没有自动维护

已具备独立目录、CRUD、同人格搜索、revision 和 kb id 链接；仍缺：

- dreaming 从 transcripts / memory 自动建页与校对；
- 重复页合并、断链修复、失效 kb id 处理；
- 可审计的重建流程与 promote / demote 策略。

### ⑤ `diary/` 是死层

原文核查时 `~/.data/xaihi/diary/` 只有一个 `2026-11-10.md`，
`grep '"diary"' --include=*.go internal/` 零命中。

应该是早期设计残留，被 `journal` 取代后没清理。建议确认后删除或合并；本次未动实际数据。

### ⑥ 多人格旧记忆归属需要人工处理（仅迁移遗留）

新的 memory/wiki 已按人格隔离。唯一人格的旧共享记忆已自动复制并保留备份；多个人格同时存在时，
旧 `memory/global` / `memory/project` 不会自动出现在任何人格中，需要人明确归属后再迁移。
这是有意避免把生活 / 工作记忆猜错主人，不是跨域读取入口。

---

## 四、注入预算的约束

论文提到 Claude Code 的自动记忆只加载 `MEMORY.md` 的**前 200 行 / 25KB** 当索引用，
并明确说：

> longer instruction files reduce adherence

旧文曾把人格卡的字节数、记忆的估算 token 数等直接相加为“约 21KB”，单位不一致，这个估算已撤回。
当前能确定的是：

- 原文统计时，已安装人格卡为 **14,865 bytes**；
- 记忆预算是默认 / 封顶 **6000 个估算 token**，不是 6000 字符或字节；
- 待办、时钟、日记提示和摘要还会增加注入量，需按实际渲染结果测量。

人格卡是“档案体”（人物设定 + 叙事），不是“索引体”。继续增加常驻内容可能降低遵循度并稀释人格。
设计时仍需决定：人格卡和操作指令是否拆开，以及哪些核心记忆才值得常驻。

---

## 五、实施顺序与当前状态

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | 原子写、跨实例 / 跨进程锁、revision、未知字段保留、路径校验、测试修复 | **已完成** |
| P1 | 自动 source、basis、updated、双向替换链、`memory_supersede`、冲突测试 | **已完成** |
| 人格结构 | 独立人格 JSON、按人格隔离 memory/wiki、工具分组、旧数据安全迁移 | **已完成** |
| P2 | 新增同人格跨会话 recall，返回 conv_id + seq；不提供跨人格搜索 | 未开始（后置） |
| P3a | 人格 dreamer、受限 reader、DreamPlan、dry-run、wiki-only apply、审计与幂等 | **已完成** |
| P3b | memory_actions 人工审核与实际 apply；定时 dreaming 与增量检查点 | 未开始 |
| P4 | 核心注入索引与完整笔记分离；全量 wiki 重建与自动维护 | 未开始 |

dreaming 初期不自动 purge、不改 persona、不直接写 kb；没有 transcript 证据时不做“纠正”。
自动 wiki 维护只在跨笔记、跨会话问题确实增多后启用，单一来源能回答的问题优先直接搜索。

---

## 六、文件索引

| 主题 | 路径 |
|---|---|
| 提示词拼装 | `internal/agent/loop.go` |
| 摘要 | `internal/agent/loop.go`（`maybeSummarize`） |
| surface 裁剪 | `internal/agent/loop.go`（`compact` / `buildSurface`） |
| 记忆与版本链 | `internal/memory/memory.go` |
| 记忆测试 | `internal/memory/memory_test.go` |
| 人格配置 | `internal/config/persona_profiles.go` |
| wiki 与版本保护 | `internal/wiki/wiki.go` |
| 工具运行时授权 | `internal/tools/access.go` |
| dreaming | `internal/dream/`；说明与后续事项见 `docs/DREAMING.md` |
| 日记 | `internal/journal/journal.go` |
| 待办 | `internal/todo/todo.go` |
| 转录存储 | `internal/store/sqlite.go` |
| 检索与人格工具 | `internal/tools/tools.go`（`recall` / `memory_*` / `wiki_*`） |
| 人格卡 | `~/.data/xaihi/personas/xaihi.md`（原文统计为 14,865 bytes） |
| 人格草稿 | `docs/persona/`（`xaihi.md` / `lore.md` / `lexicon.md` / `README.md`） |
