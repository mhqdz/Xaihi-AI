# Dreaming：手动、单人格、wiki-only 的第一条闭环

当前实现的是 Anthropic 所说 dreaming 的一个窄切片：**离线读取受限 transcripts，
让 dreamer 生成 DreamPlan，经本地校验后只应用 wiki 变更。** 它不是常驻服务，也不是完整记忆智能。

## 已确定的边界

- 人格 profile 是 memory/wiki/transcripts 的唯一归属；任何路径都不允许跨人格。
- 会话创建时固化 `persona`，可切换的生成 `profile` 不用于反推 dreaming 输入。
- 人格 JSON 的 `dreamer` 引用 profiles.json 中的会话/模型 profile。
- dreamer 只复用模型、接口和生成参数，不继承该 profile 的人格、工具或记忆注入。
- 首轮只允许写当前人格 wiki；`memory_actions` 仅输出供检查，绝不应用。
- 不修改 persona / kb，不预读或复制 kb 全文，wiki 只保存 kb id 链接。
- 不做跨会话 recall 工具、后台定时器、自动 memory apply 或全量历史调度。

## 使用

人格配置示例（`personas/xaihi.json`）：

```json
{
  "name": "xaihi",
  "memory": true,
  "wiki": true,
  "dreamer": "180-low"
}
```

`dreamer` 保存时校验引用；profile 后续被删除或缺模型/接口时，运行仍会明确报错。
前端编辑器可以配置 dreamer。**前端集中读取并展示 dreamer 运行错误暂未实现，见后续事项。**

```bash
# 默认 dry-run：只打印 diff，不写 wiki、memory 或审计文件
xaihi dream --persona xaihi --conv 1469

# 指定起始日期；RFC3339 也可以
xaihi dream --persona xaihi --since 2026-11-01 --max-tokens 20000

# 校验成功后只应用 wiki，并写审计
xaihi dream --persona xaihi --conv 1469 --apply
```

`--since` 与 `--conv` 至少一个必须显式给出，防止默认扫描全部历史。
`--max-tokens` 只限制转录输入的估算 token；已有 memory/wiki 上下文另外限量。

## transcript reader

代码：`internal/dream/transcript.go`。

- 只读未归档且 `Conversation.Persona` 与目标完全一致的会话。
- 指定其他人格的 `--conv` 时返回 `ErrCrossPersona`，不是静默返回空结果。
- 输入用户/助手正文和工具观察；跳过 system 等角色与 running 消息。
- 不输入 reasoning、图片实体或 ToolResult 中的图片/base64。
- 工具结果每条最多 800 runes，用户/助手正文每条最多 4000 runes。
- 按 `max-tokens` 截断并在结果中明确标记 `truncated`。
- 正文为空的 assistant 工具调用不是事实证据；memory 的写入 source 不自动成为有效证据。

## DreamPlan 与应用

代码：`internal/dream/types.go`、`plan.go`、`runner.go`。

```text
DreamPlan
  persona / inputs / summary
  wiki_actions:   write / delete + name/body/kb_links/revision/evidence
  memory_actions: create / supersede / archive + 来源、依据、revision、evidence
```

应用前本地校验：

- JSON 严格解析，未知字段、多个 JSON 对象、越界 persona 均拒绝；
- 每项证据必须真实存在于本次 corpus；
- wiki 页面名、kb id、正文上限、单页重复动作均校验；
- 更新/删除已有 wiki 页面必须携带当前 revision；
- 内容已经相同的 write 和目标已不存在的 delete 归为 `noop`，支持重跑幂等；
- memory 动作只解析与校验，不进入 `ApplyWiki`。

dry-run 不写任何文件。`--apply` 成功应用 wiki 后，审计写到：

```text
~/.data/xaihi/dreams/personas/<name>/<timestamp>.json
```

审计包含输入范围、token/截断状态、DreamPlan、diff 和变更数，**不保存完整转录正文**。
多动作 apply 目前不是跨文件事务：若中途文件操作失败，会返回已应用数量，审计不写；
wiki 是派生层，可据此检查后修复或重建。模型失败、非法输出或计划校验失败时不会进入 apply。

## 后续事项（本轮未做）

- 前端集中读取并展示 dreamer 配置/运行错误；
- memory_actions 的人工审核与实际 apply；
- 同人格跨会话 recall 工具；
- 自动定时 dreaming、增量检查点和更大历史的分块调度；
- wiki 全量重建、重复页语义合并、失效 kb id 检查；
- 应用失败时的跨文件回滚或暂存区提交。

## 验证

测试使用假 dreamer，不调用真实模型，覆盖：

- 同人格隔离与越权 `--conv`；
- 必填范围、预算、内容过滤和证据校验；
- dry-run 零写入、wiki-only apply、memory 不变；
- 审计不含完整转录；
- 同一计划重跑为 noop；
- 非法 JSON、失效 dreamer、revision 冲突和非法 kb id 失败关闭。
