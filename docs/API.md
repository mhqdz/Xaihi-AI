# xaihi 接口文档

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

## 接口与配置（两层）

数据分两层：**接口 → 配置**。

| 层 | 文件 | 管什么 |
|---|---|---|
| 接口 | `endpoints.json` | 接哪儿：地址、凭据、协议 |
| 配置 | `profiles.json` | 用哪个模型 + 它是什么（窗口/输出上限/模态/档位）+ 怎么用（提示词、工具、思考强度） |

**为什么模型不是单独一层**：拆过一版三层（接口 → 模型 → 配置），动机是
`180-low / 180-medium / 180-think` 共用一份模型事实，不用各写一遍窗口。
但它带来一个必须常驻的"登记"动作，而真正的痛点是这个动作，不是层本身。
现在模型事实就在配置里（读配置不需要联网，接口挂了也能算出上下文预算），
代价是同一模型的多套配置各存一份 —— 换模型/刷新上游值时在界面上点一下即可。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/endpoints` | 接口数组 |
| POST | `/api/endpoints` | 新建或覆盖（同名即覆盖） |
| POST | `/api/endpoints/probe` | **拿草稿试上游**：`{base_url, api_key?, provider?}` → `{models:[…], count}`；不通时 502 `{error}`。不落盘 |
| DELETE | `/api/endpoints/{name}` | 还有配置在用它时 400（顺手删掉这个接口的能力缓存） |
| GET | `/api/endpoints/{name}/models` | **强制问一次上游**并刷新能力缓存 → `{models:[…], at, stale:false}`；拉不到时 502 带上一份缓存 `{error, models, at, stale:true}` |
| GET | `/api/sources` | 来源模板（`sources.json`，加一条就多一个服务商） |
| PUT | `/api/sources` | 整份覆盖 |

配置（`Profile`）里和模型有关的字段：

| 字段 | 说明 |
|---|---|
| `endpoint` | 引用的接口名（必填，必须存在） |
| `model` | 发给上游的模型名，**裸 id**（不带接口前缀） |
| `context_window` | 窗口 tokens，**必填 > 0**；上游没报就自己填 |
| `max_tokens_out` | 单次回复上限，0 = 8192 |
| `input` | `["text"]` / `["text","image"]`，**必填**（至少 text） |
| `effort_levels` | 支持的思考强度标签；空 = 不限制（界面列全部五档） |
| `auto_caps` | 运行时跟随上游：每轮对话前重新问一次能力，问不到用上面存的值 |

**能力为什么必填**：留空不是"以后再拉"，而是"不知道" —— 而"以后再拉"还是同一个
结果。窗口错了会真的算错压缩预算，模态错了会让界面瞎猜能不能贴图。所以保存时就
拒掉，让填的人当场决定，而不是运行时退回一个默认值装作知道。

### 能力缓存：`model-cache.json`

不是配置层，是**上次问上游要到的答案**：`{ "<接口>": {"at": …, "models": [...]} }`。
由 xaihi 在拉列表/启动补刷时自动写，没有界面、不给人手改、删掉只会导致重拉。
用途三个：配置卡里"选模型"时的初始值、离线时的下拉列表、以及"这份数据多旧了"
（界面上的状态行永远显示 `刚刚 / N 分钟前 / N 天前`）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/profiles` | 配置数组，附带 `model_id` / `input` / `resolved_context_window` / `effort_levels` |
| POST / PUT | `/api/profiles` | 新建或更新。注意是**集合**路径，`PUT /api/profiles/{name}` 会 405 |
| DELETE | `/api/profiles/{name}` | 删除（内置也可删） |

**`api_key` 是明文**：`GET /api/endpoints`（以及 `/api/profiles` 里遗留的 key）原样返回
密钥，只在前端渲染成 `type=password`。这是有意为之 —— 单用户、家庭内网、没有鉴权系统；
打码只是表演，前端要拿它发请求，能逆向的地方永远在。理由与取舍见
[`AUDIT.md` 的 R-1](AUDIT.md#r-1-监听-000018400-且无认证)。真要收紧就做鉴权或缩回
`127.0.0.1`，不要打码。
**思考强度**：`/api/chat` 请求体可带 `reasoning_effort`，临时覆盖该配置的默认值。
不落盘 —— 配置里存的是默认值，界面改的只对这次请求生效。

### 批量提交：`POST /api/apply`

一次提交多处改动，后端按 `kind` 分发。前端把改过的东西攒成一张表单发过来，
不用自己知道"这个字段该打到哪个端点"。

```json
{"changes":[
  {"kind":"endpoint","data":{ /* 与 POST /api/endpoints 的 body 相同 */ }},
  {"kind":"profile","data":{ /* 与 POST /api/profiles 的 body 相同 */ }},
  {"kind":"plugin","name":"新名字","data":{ /* 与 POST /api/plugins 的 body 相同 */ }},
  {"kind":"tool","name":"kb_get","data":{"description":"…"}},
  {"kind":"settings","data":{ /* 与 PUT /api/settings 的 body 相同 */ }}
]}
```

接口和配置都给 `kind`：接口卡以前只能看不能存（改了没有任何地方可落），
现在它和配置走同一条路 —— 界面上两层的保存按钮也只有一级卡那一个。
写盘顺序按依赖排（接口 → 配置），报错里的"第几项"仍按请求下标。

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

内置工具：bash / screenshot / recall / view_image / spawn_task / memory_save·forget·list
（handoff_fail 只在子会话里，不在列表里暴露）。

### 触发器：本地 socket（**不在 TCP 上**）

**判断"什么时候醒"不归 agent 管。** 定时、进程出现/消失、文件变化都是确定性的，
交给 cron / systemd timer / inotifywait / 你自己的脚本；它们调这个接口，xaihi 只负责
「被叫醒之后做什么」。

```bash
xaihi trigger game-exit --data '{"app":"steam"}'
# 需要从脚本里直接打协议的话，用 socket（不是端口）：
curl -s --unix-socket ~/.data/xaihi/xaihi.sock \
  -X POST http://unix/api/trigger -H 'Content-Type: application/json' \
  -d '{"hook":"game-exit","data":{"app":"steam"}}'
```

**为什么不在 TCP 上**：触发 = 让 agent 带着 `bash` 跑一轮，而 `18400` 端口没有鉴权
（见 AUDIT.md 的 R-1）。挂在 unix socket 上，权限就等于文件权限（`0600`）——
浏览器、局域网、别的容器都够不着；而 cron / systemd timer / inotifywait / 你的 shell
本来就是本地进程，本来就该走这里。TCP 上访问 `/api/trigger` 返回 404。

| 情况 | 状态码 |
|---|---|
| 触发成功（**立刻返回**，模型在后台跑） | 200 `{ok:true,msg}` |
| 冷却中（默认 3 秒，防脚本抖动） | 409 |
| 今天配额用完（默认每钩子 10 次） | 409 |
| 没有这个钩子 / 没有 hook 字段 | 404 / 400 |

**注册与触发是两件事，用两种通道**：

| | 注册（配置层） | 触发（运行层） |
|---|---|---|
| 怎么做 | `xaihi hook add …` / `hook rm` | `POST /api/trigger` / `xaihi trigger` |
| 需要什么权限 | **本地 shell** | 只连本机端口 |
| 生效时机 | 5 秒内自动重载 | 立刻（异步跑一轮） |

**注册只走 CLI，不提供 HTTP 接口**：任何能访问 `127.0.0.1:18400` 的程序都能调
`/api/trigger`，如果注册也走 HTTP，那就等于它们能给 agent 装一个永久钩子 —— 一条提权路径。
CLI 需要的是本地 shell 权限，边界清楚得多。

**文件归 xaihi 维护**（和 `kb` 的 `.kbx` 一个规矩：工具写的，人不手改）：

```bash
xaihi hook add game-exit --prompt "我从游戏退出来了。看看有没有该提醒我的。" \
  --mode speak --quota 10 --cooldown 3
xaihi hook list
xaihi hook rm game-exit
```

它负责校验（名字只能是 `a-z0-9_-`，因为名字就是文件名；配额 1..200；冷却 0..3600）、
按固定字段顺序写 JSON（diff 干净）、5 秒内热重载。

落盘后长这样（`~/.data/xaihi/hooks/<名字>.json`），**没有"什么时候"这个字段**：

```json
{"name":"game-exit","mode":"speak","prompt":"我从游戏退出来了。",
 "quota_per_day":10,"cooldown_s":3}
```

- `mode: speak` 说话；`silent` 只做事不主动开口（提示会写在上下文里）。
- 触发后写进一个固定的「观察」会话（`kind=watch`），不碰正在聊的会话。
- 上下文里一定带 `[触发器 <名字>] 数据：{…}`，否则模型不知道为什么被叫醒。

**防失控只靠配额**（不做深度限制）：每天每钩子 10 次，用完当天不再响应。

**内置事件源**：观察器（`internal/watch`）检测到窗口消失时，会调**同一个**触发入口
（钩子名 `app-close`，数据是 `{app,title,pid}`）。所以内置事件和你自己的脚本完全同权：
没定义 `app-close` 就安静跳过，定义了才走配额与冷却。

```json
// ~/.data/xaihi/hooks/app-close.json
{"name":"app-close","mode":"speak",
 "prompt":"我关掉了一个窗口（见数据）。异常退出就提一句，正常关窗别打扰我。"}
```

**silent 模式**：只做事、不主动开口（提示里会加一句"这是静默触发"）。
实测例子：触发后模型真的执行了 `cp`（工具返回 exit 0、文件生成），回复只有一个「好」。

```json
{"name":"backup-demo","mode":"silent","quota_per_day":3,
 "prompt":"用 bash 把 ~/.data/xaihi/profiles.json 复制到 /tmp/xaihi-backup.json，做完只回一个词：好。"}
```
链条再长也会自己停。

**长期记忆**（`internal/memory`）：一条记忆 = `~/.data/xaihi/memory/global/<id>.md`，
front matter 是 `id / created / scope / kind / status / source`，正文人能直接改。
每轮请求把 active 记忆按 token 预算（默认 6000）拼进 system prompt：挑的时候从新到旧、
**拼的时候排回升序**（顺序不稳定会让上游 KV 缓存全部作废）。`memory_forget` 只标
`archived`（文件留着，可恢复），真删是人工的 `Purge`。和 kb 的分工：**需要一直在场的
→ 记忆；只是可能有用 → kb**。

```json
[{"name":"bash","source":"builtin","path":"","timeout_ms":0,"persistent":false},
 {"name":"kb_search","source":"plugin","path":"~/.data/xaihi/tools.d/kb_search.json","timeout_ms":20000,"persistent":false}]
```

- `source=builtin`：Go 内置（`internal/tools/tools.go` 里用 Spec 指定），编译期确定
- `source=plugin`：外置插件（`~/.data/xaihi/tools.d/*.json` 清单 + 可执行脚本），改完即生效、无需重编译

清单格式：

```json
{"name":"kb_get","description":"…","params":{…},"required":["id"],
 "command":["~/Workspace/xaihi/scripts/tools/kb_get"],"timeoutMs":20000}
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
| DELETE | `/api/plugins/<name>` | 删除清单（只删 `~/.data/xaihi/tools.d/<name>.json`，脚本文件保留）；内置工具拒绝 |

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
| GET | `/shots/<name>.png` | 直接取截图文件（`~/.data/xaihi/shots/`） |

消息里只存 `/shots/<name>.png` 路径；发给模型时由 `toWire` 读盘转 data URL。
自动清理：`settings.shots_auto_clean` + `shots_keep_days`（默认开 + 7 天），
启动与每小时巡检时执行一次。
