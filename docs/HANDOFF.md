# 交接与唤醒（handoff）

一句话：**让 agent 能把一件事交出去、在别处跑完、再回来收尾**——换模型、让出显卡、定时自检，都走同一条链路。

## 1. 概念

```
父会话 conv#10 ──spawn_task(kind, card)──▶ task#7 ──建子会话──▶ conv#23（↳#1）
      ▲                                                          │
      └────── outbox 消息（执行记录 + 产物）── 会话内 FIFO ────────┘
```

| 概念 | 说明 | 存在哪 |
|---|---|---|
| 配方 recipe | 一类任务的命令序列 + 收尾命令 | `~/.data/xaihi/tasks.d/<kind>.json`（人写，模型不现编） |
| 交接卡 card | 目标 / 输入 / 约束 / 产物 / 验收 | `~/.data/xaihi/handoff/<task>.json` |
| 任务 task | 一次派发的状态机 | DB `tasks` |
| 步骤 step | 每条命令/子会话的执行结果（像工具输出） | DB `task_steps` |
| outbox | 待投给会话的系统消息（先入库、后投递） | DB `outbox` |

## 2. 明确不做

```
· 资源探测 / 租约 / 锁 / 让路提示：不做。抢卡了就失败，过程可见即可
· 排队 / 互斥：不做（不同任务并发是允许的）
· 自动续跑：xaihi 重启 → 进行中的任务标 interrupted，人工点「收尾」
· 命令白名单：不做，配方就是 shell，正确性由写配方的人负责
· 外部编排插件：不做，编排在 xaihi 内置
```

## 3. 配方 schema

```json
{ "kind": "comfy-render",
  "description": "让出 2080ti 渲染一批图，结束后把 180 切回来",
  "profile": "9-exec",
  "max_rounds": 30,
  "timeout_s": 3600,
  "steps": [
    { "run": "llm-local use 9b" },
    { "run": "sleep 30" },
    { "run": "systemctl --user start comfyui" },
    { "spawn": true }
  ],
  "restore": [
    { "run": "systemctl --user stop comfyui" },
    { "run": "llm-local use 180" }
  ],
  "artifacts": ["/tmp/render/*.png"]
}
```

- `steps[].run`：`bash -c` 执行，单步超时 `timeout_s`（默认 600）
- `steps[].spawn: true`：在此处创建子会话并把交接卡发过去（同步等到子会话这一轮结束）
- `restore`：**无论成败都执行**；失败自动重试 3 次，仍失败记 `restore_failed`
- 模型只能派发**已注册**的 kind（`spawn_task` 工具与 `/api/tasks` 同一套校验）

## 4. 数据模型

```sql
ALTER TABLE conversations ADD COLUMN parent_id INTEGER NOT NULL DEFAULT 0;  -- 0 = 顶层
ALTER TABLE conversations ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat';     -- chat | child
ALTER TABLE messages ADD COLUMN origin TEXT NOT NULL DEFAULT 'user';        -- user | system | timer | handoff

CREATE TABLE tasks(
  id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL,
  parent_conv INTEGER NOT NULL, child_conv INTEGER NOT NULL DEFAULT 0,
  card_path TEXT, status TEXT NOT NULL,        -- running|review|done|failed|interrupted|cancelled
  error TEXT, created_at TEXT, finished_at TEXT);
CREATE TABLE task_steps(
  id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER NOT NULL, seq INTEGER NOT NULL,
  name TEXT, kind TEXT,                        -- run | spawn | restore | artifact
  cmd TEXT, exit_code INTEGER, out_chars INTEGER, ms INTEGER, status TEXT, output TEXT);
CREATE TABLE outbox(
  id INTEGER PRIMARY KEY AUTOINCREMENT, conv_id INTEGER NOT NULL,
  origin TEXT NOT NULL, status TEXT NOT NULL,  -- pending | delivered | failed
  text TEXT NOT NULL, created_at TEXT, delivered_at TEXT);
```

显示：子会话 `↳#<seq>`（该父会话下第几个子会话）；主键仍是整数。

## 5. 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/tasks` | 手动派发 `{kind, conv_id, card}`（前端/脚本用，和 `spawn_task` 同一套校验） |
| POST | `/api/wake` | `{conv_id, text, origin}`；Header `X-Xaihi-Token`（`~/.data/xaihi/wake.token`）；写 outbox → 立即返回 id |
| GET | `/api/tasks?conv=&status=` | 任务列表（含子会话编号） |
| GET | `/api/tasks/{id}` | 详情：配方、步骤、产物、outbox |
| POST | `/api/tasks/{id}/cancel` | 取消（尽力：kill 当前步骤；任务最终状态会落到 `failed` + `exit -1`） |
| ~~POST `/api/tasks/{id}/retry`~~ | **未实现**（调用会得到 `404 未知操作`）；要重跑就重新派发一次 `POST /api/tasks` |
| POST | `/api/tasks/{id}/restore` | 手动执行收尾（interrupted 用） |
| ~~POST `/api/outbox/{id}/deliver\|cancel`~~ | **未实现**（没有这个路由）；outbox 由 `ConsumeOutbox` 自动消费 |

工具：`spawn_task(kind, card)`（父会话）、`handoff_fail(reason, evidence)`（子会话，只报事实）。

## 6. 状态机

```
派发 → running ──步骤全 ok + spawn ok──▶ review（已回报父）──▶ done
           │
           ├─ 步骤失败 ──▶ restore（重试3）──▶ failed（仍回报父）
           └─ xaihi 重启 ──▶ interrupted（人工点「收尾」→ restore → failed）
```

## 7. 消息传递（不经子模型转述）

```
L1 执行记录（机械生成）：每步 name / 退出码 / 耗时 / 输出摘要（≤120字，完整落盘给路径）
L2 子会话最后一段正文（截断 2k 字）
L3 指针：子会话 id（↳#N），父需要细节可用 bash/recall 去读
```
父会话复核产物：用内置工具 `view_image(path)`（png/jpg/webp/gif ≤8MB，支持通配符），
工具结果里带 data URL，模型能直接看到像素。

子会话失败时**不解释原因**：`handoff_fail(reason, evidence)` 只写事实，父会话自己判断。

## 8. 前端

```
任务视图：列表（↳#N · kind · 状态 · 耗时）→ 详情（步骤表 ✓/✗/耗时/输出、产物、[重试][取消][收尾]）
会话内：outbox 消息按系统提示样式渲染，pending 显示"排队中"，可 [取消]
子会话：正常会话样式 + 标题前 ↳#N，可手动继续聊
```

## 9. 失败矩阵

| 场景 | 处理 |
|---|---|
| 命令失败 | 该步 failed；后续步骤跳过；restore 照跑；回报父 |
| spawn 的子会话失败 | 记录子会话 id 与错误；restore 照跑；回报父 |
| restore 失败 | 重试 3 次；仍失败 → `restore_failed` + 红条 + 日志 |
| 产物缺失 | 记 `artifact:missing`，任务 failed |
| 父会话不存在/已删 | 回报落到该会话（会话不存在则丢弃并记日志） |
| 父会话归档 | 投递时自动取消归档 |
| xaihi 重启 | running → interrupted；不自动收尾，任务视图给「收尾」按钮 |

## 10. 分期

```
M1（已实现）：配方（热加载）+ spawn_task + 子会话 + 步骤/产物/收尾 + 执行记录 + outbox（会话内 FIFO）
              + /api/wake（token）+ /api/tasks（列表/详情/手动派发/收尾）+ 前端任务区
   已验证：go test ./... 全绿（handoff 单测 + web 交接测试）；E2E 用 smoke 配方跑通
          （3 条命令 + sleep + 产物检查 + restore + 回报 outbox delivered）
M1 已验证的链路（跨本地/云端互调，2026-10-29）：
  · 本地父（180-low）→ 云端子（ds-flash）：子会话回复 CLOUD-CHILD-OK，父会话收到回报并复核 ✓
  · 云端父（ds-flash）→ 本地子（180-low）：父模型调 spawn_task，子会话 LOCAL-CHILD-OK ✓
  · 云端父 → 云端子（ds-flash）：CLOUD-TO-CLOUD-OK ✓
  · 本地父 → 本地子（180）：spawn-demo ✓
  子会话 profile 只影响模型与工具（工具固定 bash+handoff_fail），provider 差异（reasoning 扩展）在 agent 层处理 ✓
  · 命令步骤 + sleep + 产物检查 + restore → task done，执行记录入 outbox
  · spawn 成功（子会话 conv#66 回复 "ok"，父会话拿到 ↳#66 与结尾正文）
  · spawn 失败（9B 后端未起 → upstream 503，restore 照跑，记录里带原始错误）
  · /api/wake（token）→ outbox → 会话内 FIFO 投递 → 消息 origin=timer，界面按系统提示渲染
  · 重启：running → interrupted（启动时日志提示），任务区给「执行收尾」按钮

前端排版与交互已用 WebDriver（`scripts/ui-test/`）自动验证：宽屏三卡/互斥/同宽、窄屏单卡、
思考展开无内滚与收起回滚、任务展开与深链滚动、会话打开在底部、最后一条消息不被输入框遮挡、JS 无错误。

M1 已知缺口：任务详情需点击展开才加载；无重试/取消按钮；任务列表未按父会话过滤；
             投递与父会话的 profile 绑定（KV.profile），旧会话没有该字段时用默认 profile
M2：cancel / retry / restore 按钮、产物缩略、任务通知
M2 已完成：`view_image(path)`（本地图片 → data URL，父会话复核产物用；已加进 180 系列 profile）
M3：任务重试/取消按钮、按父会话过滤任务列表、定时任务共用一个"日程"会话的约定
```
