# Wiki / KB / 人格 / 记忆：开发路线图

> 用途：收束当前实现，确定下一步顺序。以当前代码和 `MEMORY-STATUS.md`、`DREAMING.md` 为准。
> 当前处于开发期，旧测试数据与旧兼容迁移不再作为投入重点。

## 一、已经确定的结构

```text
会话 / 模型 profile（profiles.json）
  模型、接口、生成参数、工具选择
        │ 引用
        ▼
人格 profile（personas/<name>.json + <name>.md）
  人格正文、memory / wiki / dreamer 配置
        ├── memory/personas/<name>/   自己的笔记，会腐坏，需要证据与版本链
        ├── wiki/personas/<name>/     自己的派生索引，可整理、可重建
        └── transcripts 中 persona=<name> 的会话

kb：唯一共有知识层；人审、agent 只读，通过 id 链接。
```

核心约束：

- 多个人格之间不共享 memory/wiki，不提供跨人格搜索。
- 同一人格的多个会话 profile 可以换模型，但共享 memory/wiki。
- memory 不是 kb，wiki 也不是 memory 的替代品。
- dreaming 是离线整理过程；wiki 建页/重建只是其中一种输出。
- 工具是否可见由会话 profile 选择与人格能力共同决定；空 `Tools=[]` 表示无工具。
- 这里的隔离是正常工具路径下的逻辑隔离；不受限 `bash` 不等于安全沙箱。

## 二、已经完成

### A. memory：可靠存储与版本链

- Markdown + front matter；`active / superseded / archived`。
- `source` 自动记录 `conv:<id>#<seq>`；`basis=explicit|verified|inferred`。
- `updated / supersedes / superseded_by`。
- 原子写、跨实例锁、跨进程 `flock`、文件 SHA-256 revision。
- 替换检查 revision，保留旧正文和未知 front-matter 字段。
- `memory_save / memory_list / memory_supersede / memory_forget`。
- 默认/封顶 6000 个估算 token 的注入预算，旧 memory 暂不改变注入策略。

### B. 人格与工具归属

- 会话/模型 profile 与人格 profile 已分开。
- 人格正文使用 `personas/<name>.md`，能力配置使用同名 `.json`。
- 人格 JSON 支持 `memory / wiki / dreamer`。
- memory/wiki 按人格独立目录；旧 `persistent` 保留给 hook/journal/todo。
- 新增“人格”工具组；运行时统一检查人格能力、persistent 和图像能力。
- 空 `Tools` 隐式开放全部工具的问题已修复。
- 人格配置损坏时失败关闭；修改 JSON 前保留 `.bak`。
- body-only 的旧人格 API 请求不会主动生成或覆盖人格配置。

### C. wiki：独立派生层

- 每个人格一套独立 wiki，不注入 system prompt。
- 页面为 Markdown，含 `name / updated / kb`；kb 仅保存 id 链接。
- `wiki_list / wiki_search / wiki_read / wiki_write / wiki_delete`。
- 更新和删除检查 revision；搜索只限当前人格。
- 已有多人格隔离、CRUD、并发冲突和工具端到端测试。

### D. dreaming 第一阶段：wiki-only 闭环

- 人格可固定 `dreamer`，保存和运行时都校验引用。
- dreamer 只复用模型参数，不继承人格、工具或记忆注入。
- 会话创建时固化 `persona`；新会话、handoff、观察收件箱均覆盖。
- transcript reader：
  - 必须显式提供 `--since` 或 `--conv`；
  - 同人格过滤、越权报错、token 预算与截断标记；
  - 用户/助手正文 + 截断工具观察；
  - 排除 reasoning、图片/base64、running 消息。
- DreamPlan：
  - `wiki_actions=write/delete`；
  - `memory_actions=create/supersede/archive`，仅输出、不应用；
  - 证据必须真实存在于本次 corpus；
  - wiki 更新/删除检查 revision，非法动作失败关闭。
- 相同 write 与已删除目标归为 `noop`，支持重跑幂等。
- 默认 dry-run 零写入；`--apply` 只写 wiki。
- apply 后写审计，不包含完整转录正文。
- CLI：

```bash
xaihi dream --persona xaihi --conv 1469
xaihi dream --persona xaihi --since 2026-11-01 --max-tokens 20000
xaihi dream --persona xaihi --conv 1469 --apply
```

### E. 测试与文档

- 相关包 race、全仓库 `go test ./...`、`go vet ./...` 已通过。
- 前端 `npm run build` 已通过。
- 已覆盖人格隔离、memory 版本冲突、wiki CRUD、dry-run 零写入、wiki-only apply、
  memory 不变、审计不含完整转录、失效 dreamer、非法 JSON 和重复运行幂等。
- 当前说明：`MEMORY-STATUS.md`、`DREAMING.md`、`API.md`。

## 三、尚未完成

### 最高优先：真实输出质量未知

- 目前端到端测试使用假 dreamer，尚未做真实模型冒烟与质量评估。
- 不知道真实模型会更倾向于“主题索引”还是“对话摘要”。
- 不知道页面粒度、重复率、证据表达、调用成本和重跑稳定性是否可接受。

### dreaming 完整性

- `memory_actions` 没有人工审核与实际 apply。
- 没有自动定时、增量检查点、断点续跑和大历史分块。
- 多动作 apply 不是跨文件事务；中途失败只报告已应用数量。
- 没有全量 wiki 暂存重建、事务替换或失败回滚。
- 前端集中展示 dreamer 配置/运行错误未做。

### wiki 质量与维护

- 没有语义级重复页合并、主题归属评估和页面粒度控制。
- 没有失效 kb id 检查、断链修复或 kb 内容按需核对。
- 没有全量重建、重建前后质量对比和可恢复版本。
- 没有“map 是否比原始 search 更有价值”的任务级评测。

### recall 与注入

- `recall` 仍只搜当前会话；同人格跨会话 recall 未做。
- 明确不做跨人格搜索，也不做 `scope=all`。
- active memory 仍全部参与注入筛选；核心常驻索引与完整笔记尚未分离。
- 没有 dreaming 驱动的 promote/demote 和注入遵循度评测。

### kb 与人格周边

- kb 仍是只读外部事实，尚无 memory → kb 的人工审校草稿/promote 流程。
- 人格正文尚未定稿，但不阻塞记忆管线；不应把人格写作与 dreaming 实现绑在一起。
- 多人格旧测试数据归属不再作为重点，不继续增加兼容迁移复杂度。

## 四、推荐路径

### 阶段 1：真实冒烟 + 最小评测（当前最高优先级）

目标：先证明 dreaming 的输出有用，再扩大功能。

1. 建一个专用测试人格和 dreamer，准备 3–5 段小型受控会话：
   - 明确偏好；
   - 被后续纠正的错误事实；
   - 同一项目跨会话经验；
   - 工具观察验证的规则；
   - 另一个人格的“金丝雀”内容，验证绝不混入。
2. 先 dry-run，人工检查 DreamPlan，再 `--apply`。
3. 连续运行两次，检查第二次是否零变更。
4. 记录每次输入/输出 token、耗时、页面数、动作数和人工发现的问题。
5. 只根据真实失败调整 prompt、页面模板和校验，不先加调度、向量库或全量历史。

通过标准：

- 跨人格泄漏为 0；所有动作有可访问证据；
- wiki 是简洁主题索引，不是逐日 transcript 摘要；
- 无来源内容不进入 wiki/memory 计划；
- 同一输入重跑无重复页面、无 revision 冲突；
- dry-run 磁盘不变，apply 不修改 memory/kb/persona。

### 阶段 2：把 wiki 做成可长期使用的表示层

目标：可靠、简洁、可恢复，而不是页面越多越好。

1. 固定页面类型和模板：`index`、人物/项目/问题/经验/来源。
2. 加页面去重与命名约束，减少同一主题多页。
3. 做暂存区全量重建、diff、备份和失败恢复；不直接覆盖唯一可用版本。
4. 加 kb id 存在性检查与断链报告；需要全文时按需读取，不批量复制 kb。
5. 建小型任务对照评测：原始 transcript search、wiki-only、wiki+search 三者比较。

只有 wiki+search 在跨文档问题上有收益时，才继续增加 wiki 自动维护成本。

### 阶段 3：memory 人工审核与 apply

目标：完成“记忆会腐坏，也能被证据纠正”的闭环。

1. 从审计文件读取 DreamPlan，新增独立审核命令，不默认和 wiki apply 捆绑。
2. 展示 create/supersede/archive 的证据、正文、旧版本和 diff。
3. apply 时再次读取 memory 并检查 revision，冲突则拒绝，不用计划里的旧状态强行覆盖。
4. create 去重；supersede 保留双向链；archive 仅软删除；继续禁止自动 purge。
5. 记录 memory 专用审计；失败支持恢复，重复 apply 保持幂等。

建议命令语义：

```bash
xaihi dream review <audit-id>
xaihi dream apply-memory <audit-id>
```

### 阶段 4：同人格跨会话 recall（后置）

目标：让 agent 按需找到原始证据，不扩大数据边界。

- 新增独立、可选的 `memory_recall`，归入人格工具组。
- 现有 `recall` 继续只搜当前会话。
- 服务端固定当前 persona，只搜同一人格；不提供目标 persona、domain 或 `scope=all`。
- 返回 `conv_id + seq + 时间 + 标题 + 片段`；`around` 仍复核 persona。
- 默认关闭，无自动搜索、无自动注入、无后台模型调用。
- 优先复用 dreaming 的归属与过滤约束，先做受限 SQLite 查询，按真实性能再决定索引。

### 阶段 5：增量调度与注入层优化（最后）

- 按 `(persona, conv_id, seq)` 记录检查点，失败可续跑。
- 先做手动触发/外部单次任务，稳定后再考虑定时；不直接上常驻 dreaming。
- 每次有范围、预算、锁和审计；模型失败不得阻塞正常对话。
- 将 memory 分成“小型核心注入索引”和“完整按需笔记”，再做 promote/demote。
- 用遵循度和任务结果评估注入，不以记忆数量或 wiki 页面数量作为成功指标。

## 五、暂时不要做的事

- 不做跨人格 recall、`scope=all` 或全局共享 wiki。
- 不让 dreaming 直接改 kb/persona，不自动 purge memory。
- 不先做向量数据库、复杂 embedding 或全量历史无预算扫描。
- 不在真实输出质量未验证前做定时调度和大规模重建。
- 不把“生成很多记忆/页面”当成收益；冗余本身就是失败。
- 不继续为开发期旧测试数据设计复杂迁移。

## 六、当前最小下一步

只做这一阶段：

> **真实 dreamer 冒烟 + 受控小型评测 + prompt/页面模板迭代。**

暂不同时启动 memory apply、recall、定时任务和全量重建。等真实 wiki 输出证明“简洁、可追溯、可复用”后，再进入阶段 2。
