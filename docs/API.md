# agentd 接口文档

> 监听地址：默认 `127.0.0.1:18400`，用 `AGENTD_ADDR` 覆盖（当前部署为 `0.0.0.0:18400`，
> 局域网访问 `http://192.168.1.200:18400`）。**无鉴权**：能访问端口的人都能用会话里的工具（含 bash），
> 只应在可信网络里开放。

约定：全部 JSON（除 SSE 与静态文件）；错误统一 `{"error":"说明"}` + 相应状态码；无鉴权（仅监听 127.0.0.1）。

## 健康与运行态

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | `{"ok":true}` |
| GET | `/api/running` | 正在生成中的会话 id 数组，如 `["8","9"]` |

## 会话

`GET /api/convs` 每行：`{ID, Title, UpdatedAt, N, ParentID, Kind, Seq}`。子会话 `Kind="child"`、
`ParentID` 指向父会话、`Seq` 是在同父下的序号（界面显示 ↳#N）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/convs` | 未归档列表：`[{ID,Title,UpdatedAt,N}]`（N=消息条数） |
| GET | `/api/convs?archived=1` | 归档列表，同结构 |
| GET | `/api/conv/{id}` | 会话全文：`{id,title,kind,kv,summary,summary_upto,summary_model,summary_at,created_at,updated_at,page,messages:[{seq,role,content,reasoning,reasoning_ms,tool_calls,tool_result,created_at}]}`；`page` 是分页窗口 `{from,to,total_rounds,remaining_rounds,has_more,last_seq,rounds,total_messages}` |
| DELETE | `/api/conv/{id}` | 归档（只有 DELETE；`POST .../archive` 会 405） |
| POST | `/api/conv/{id}/restore` | 恢复 |
| POST | `/api/conv/{id}/interject` `{"text":"…"}` | 向进行中的轮次插话（下一轮工具调用前注入） |
| POST | `/api/conv/{id}/stop` | 中断当前生成（后台任务取消，已生成内容落库） |
| GET | `/api/conv/{id}/stream?from=N` | 断线重连：轮次在跑时重放缓冲区第 N 帧起并续流；**不在运行则直接返回 `event: done`（200）**，不回放旧帧 |

## 对话（SSE）

`POST /api/chat` `{"conv_id":"","text":"…","profile":"180-low"}`

- 立即返回 `text/event-stream`；后台任务不绑定 `r.Context()`，浏览器断开不中断生成。
- 事件：

| event | data | 说明 |
|---|---|---|
| `meta` | `{conv_id,title}` | 会话已创建/确定，前端据此写 URL |
| `reasoning` | `{text}` | 思考增量 |
| `delta` | `{text}` | 正文增量 |
| `tool` | `{name,args,text,image}` | 一次工具调用结束（text=输出摘要，image=是否含图） |
| `interject` | `{text}` | 已收到插话 |
| `error` | `{error}` | 出错（已在会话里落半截内容 + 中断说明） |
| `done` | `{conv_id,secs}` | 完成 |

## 设置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/settings` | `{tool_rounds_enabled,tool_rounds,auto_archive_enabled,auto_archive_days,shots_auto_clean,shots_keep_days}` |
| PUT | `/api/settings` | 同上结构；自动归档 / 截图清理相关字段变化时立即触发一次巡检 |

截图：`GET /api/shots/stats` → `{count,size_mb}`；`POST /api/shots/cleanup {"days":7}` → `{removed,freed_kb,days}`。

## 模型与 profile

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/models?base_url=&api_key=` | 代理上游 `/v1/models`：`{"models":["…"]}`；非 200 时返回 `{"error":"接口返回 N（检查地址或 API Key）"}` |
| GET | `/api/profiles` | profile 数组 |
| POST / PUT | `/api/profiles` | 新建或更新（`api_key` 为空则回落全局配置）。注意是**集合**路径，`PUT /api/profiles/{name}` 会 405 |
| DELETE | `/api/profiles/{name}` | 删除（内置也可删） |

### 批量提交：`POST /api/apply`

一次提交多处改动，后端按 `kind` 分发。前端把改过的东西攒成一张表单发过来，
不用自己知道"这个字段该打到哪个端点"。

```json
{"changes":[
  {"kind":"profile","data":{ /* 与 POST /api/profiles 的 body 相同 */ }},
  {"kind":"tool","name":"kb_get","data":{"description":"…"}},
  {"kind":"settings","data":{ /* 与 PUT /api/settings 的 body 相同 */ }}
]}
```

返回：

| 情况 | 状态码 | body |
|---|---|---|
| 全部成功 | 200 | `{ok:true, applied:N}` |
| 某一项校验失败 | 400 | `{error, index, kind, name, applied:0}` |
| 写入时 IO 失败 | 500 | `{error, index, …, applied:N}` |

**分两阶段**：先把每一项解析并校验完（一个问题都不写盘），全部通过才开始写。
所以「第 2 项字段非法」不会让第 1 项白写进去 —— `applied` 始终准确反映真实落了
几条。`index` 从 0 开始，前端据此指出是哪一条出错。

上限 200 项；超过返回 400。和其它写接口一样要求
`Content-Type: application/json`（缺了返回 415）。

profile 里与上下文/生成相关的字段：

| 字段 | 默认 | 说明 |
|---|---|---|
| `system` | — | 系统提示（唯一来源，前端可编辑） |
| `tools` | `[]` | 该模型可用的工具名（内置 + 插件，统一在这里控制） |
| `keep_messages` | 16 | 压缩时至少保留的最近消息条数 |
| `max_prompt_tokens` | 105000 | 输入 token 上限（内部按 ×1.8 换算字符） |
| `context_window` | 0（自动） | 模型窗口；0 = 读上游 `meta.n_ctx` |
| `compact_pct` | 80 | 达到上限的百分之几触发摘要/裁剪 |
| `max_tokens_out` | 8192 | 单轮输出上限（含思考），防止跑飞 |
| `reasoning_budget` | 4096 | 单轮思考 token 预算（上游 `reasoning_budget_tokens`） |
| `summarizer` | 空（自己） | 用哪个 profile 写摘要 |
| `reasoning_effort` | — | none/low/medium/high/max，透传上游（云端 none 时省略） |
| `provider` | 空（本地/兼容） | `openai` = 云端兼容接口：不发 `reasoning_budget_tokens`，窗口读 `/v1/models` 的顶层 `context_window` |

## 静态

`GET /` 及其它未匹配路径 → `web/dist`（`Cache-Control: no-store` 对 html）；`/fonts/*`、`/assets/*` 直出。

## 示例

```bash
curl -s http://127.0.0.1:18400/api/settings
curl -s -N -X POST http://127.0.0.1:18400/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"conv_id":"","text":"列出当前目录","profile":"180-low"}'
curl -s http://127.0.0.1:18400/api/conv/9 | jq '.messages[-1]'
```

## 工具

`GET /api/tools` → 每个工具含 `name / description / params / source / path`：

内置工具：bash / screenshot / recall / view_image / spawn_task（handoff_fail 只在子会话里，不在列表里暴露）。

```json
[{"name":"bash","source":"builtin","path":"","timeout_ms":0,"persistent":false},
 {"name":"kb_search","source":"plugin","path":"~/.data/agentd/tools.d/kb_search.json","timeout_ms":20000,"persistent":false}]
```

- `source=builtin`：Go 内置（`internal/tools/tools.go` 里用 Spec 指定），编译期确定
- `source=plugin`：外置插件（`~/.data/agentd/tools.d/*.json` 清单 + 可执行脚本），改完即生效、无需重编译

清单格式：

```json
{"name":"kb_get","description":"…","params":{…},"required":["id"],
 "command":["~/Workspace/agentd/scripts/tools/kb_get"],"timeoutMs":20000}
```

常驻形态：清单加 `"persistent": true` → 进程只启动一次，走 JSON-RPC 2.0 over stdio
（`initialize` / `tools/list` / `tools/call`），**一个进程可以暴露多个工具**（与 MCP 同构）。
`tools/list` 返回的 `{name,description,params,required}` 会覆盖清单里的同名字段；
结果支持 `{"content":[{"type":"text","text":…},{"type":"image","url":"data:…"}]}`。
示例：`scripts/tools/demo_persist.py` + `scripts/tools.d/demo_persist.json.example`。

一次性形态：直接 exec（不经 shell）；参数以 `AGENTD_ARG_<NAME>` 环境变量传入（数组用换行连接），
stdin 同时给一份 `{"tool":"…","args":{…}}`；stdout 为纯文本，或 `{"text":…,"images":[…]}`。
可用性仍由 `profiles.json.tools` 决定。

### 插件的增删

| 方法 | 路径 | 说明 |
|---|---|---|
| POST/PUT | `/api/plugins` | 保存清单（name/description/command/timeoutMs/persistent/params/required）；同名内置工具会被拒绝 |
| DELETE | `/api/plugins/<name>` | 删除清单（只删 `~/.data/agentd/tools.d/<name>.json`，脚本文件保留）；内置工具拒绝 |

`PUT /api/tools/<name> {"description":"…"}`：内置工具写 `tools.json` 覆盖；外置插件直接改清单里的 description。

消息里的工具耗时：`messages.tool_ms`（毫秒，仅 tool 消息有），前端在工具卡头部显示 `· 11ms`。

## 上下文统计

`GET /api/context?conv=<id>&profile=<name>`

```json
{"chars":89677,"max":189000,"est_tokens":49820,"limit_tokens":105000,"window":131072,
 "chars_per_token":1.8,"compact_pct":80,"compact_at":84000,"raw":92497,
 "messages":43,"summary":false,"pct":47.4}
```

- 单位：接口对外用 **token**（配置也是 token）；内部实际裁剪按**字符(rune)**，`chars ÷ 1.8 ≈ tokens`
- `limit_tokens`：该 profile 的输入上限（`max_prompt_tokens`，默认 105000）
- `window`：模型窗口，优先 `profiles.json.context_window`，否则读上游 `GET /v1/models`（10 分钟缓存）：
  本地引擎看 `meta.n_ctx`，云端兼容接口看顶层 `context_window` / `context_length`；都没有才兜底 131072
- `compact_pct` / `compact_at`：达到上限的百分之几（默认 80）触发摘要 + 裁剪
- `chars` / `max`：压缩后的字符数；`raw`：未压缩的原始字符数（消息正文 + 思考）

## SSE 事件（`/api/chat` 与 `/api/conv/<id>/stream`）

| event | data | 说明 |
|---|---|---|
| `meta` | `{conv_id,title}` | 会话建立/标题 |
| `reasoning` | `{text}` | 思考增量 |
| `delta` | `{text}` | 正文增量 |
| `stage` | `{stage:"compacting"\|"generating"}` | 阶段提示（压缩上下文 / 开始生成） |
| `tool_start` | `{name,args}` | 工具开始执行（前端显示"执行中…"计时卡，长工具不再看起来像卡死） |
| `tool` | `{name,args,text,image,ms}` | 一次工具调用完成（`ms` 是工具自身的实测耗时） |
| `interject` | `{text}` | 本轮接收到的插话回显 |
| `usage` | `{tps,completion,prompt,cached}` | 上游 `usage/timings`（右栏 tok/s 用） |
| `error` | `{error}` | 本轮失败/被中止 |
| `done` | `{secs}` | 本轮结束 |

补充：流期间服务端每 5 秒发 `: ping` 注释帧，避免长工具执行时连接被判空闲断开。

## 插话

`POST /api/conv/<id>/interject {"text":"…"}` → `{"ok":true}`

- 有工具轮次时：下一轮开始前注入上下文，模型本轮即可看到
- 本轮没有工具轮次：结束时落库成一条 `user` 消息，下一轮生效（不会丢）

## 截图文件

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/shots/stats` | `{count, size_mb}`：截图目录的文件数与占用 |
| POST | `/api/shots/cleanup` `{"days":7}` | 删除超过 N 天的截图，返回 `{removed, freed_kb, days}` |
| GET | `/shots/<name>.png` | 直接取截图文件（`~/.data/agentd/shots/`） |

消息里只存 `/shots/<name>.png` 路径；发给模型时由 `toWire` 读盘转 data URL。
自动清理：`settings.shots_auto_clean` + `shots_keep_days`（默认开 + 7 天），
启动与每小时巡检时执行一次。
