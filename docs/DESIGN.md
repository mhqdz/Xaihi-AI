# agentd 设计理念

一句话：**单机、单二进制、可审计的本地 agent**——把"模型 + 工具 + 会话"三件事留在本机，
用最小依赖换来可解释、可回滚。

## 1. 技术选型
- Go 标准库为主，唯一外部依赖 `modernc.org/sqlite`（免 cgo）。理由：部署是一个二进制 + 一个 db 文件，
  不引入包管理器世界。
- 前端 Vite + React，不引 UI 框架：视觉由一份手写 `theme.css` 控制（见 `UI.md`），
  避免"框架默认样式 + 层层覆盖"。

## 2. 动态状态不落库
"正在生成""当前第几轮""任务缓冲区"只在内存；数据库存用户可见的事实（消息、标题、时间、归档位）。

- **消息状态是落库的**：`messages.status` 取 `running` / `done` / `failed` / `interrupted`。
  早期设计是"不看状态位、靠数据推断"，后来为了让失败和中断在界面上能区分（以及避免前端
  猜不出"这条到底完了没有"），还是把它落了库。
- 代价与兜底：进程重启时 `running` 会变成"僵尸"，所以启动时扫一遍未完成的流式消息，
  把 `running` 改写成 `interrupted`（并打一条 WARN）。也就是说**状态位只在本次进程内可信**。
- 好处：重启后不会出现永远转圈的"正在生成"；坏处：重启丢流（用 `/api/conv/{id}/stream?from=N` 兜 5 分钟缓冲）。

## 3. 生成与连接解耦
`/api/chat` 的后台任务用 `context.Background()`，不绑请求上下文：浏览器刷新/断网不打断生成。
取消只能显式 `POST /api/conv/{id}/stop`。SSE 帧进按会话保存的缓冲（`internal/web/job.go`），供重连回放。

缓冲不是环形数组，而是一个随轮次增长的 `[]string`：一轮结束后保留 5 分钟（`AGENTD_JOB_TTL` 可覆盖），
期间 `/stream?from=N` 可以按帧号重放；TTL 到期或下一轮 `reset()` 时清空。
单轮帧数受输出长度限制，实测 60KB 的回复也就几千帧、几百 KB，不构成内存压力。

## 4. 工具：内置 + 外置插件（2026-10-28 起）

（2026-10-25 曾决定"不引入插件体系"，理由是体量不值得；后来 kb/web 四个工具明显更适合脚本形态，
于是改成两层：内置留在 Go 里，脚本类工具走清单 + 进程协议。）
工具 = 一个 `Spec`（name/description/JSON Schema/Required/Run）。理由见 `PLAN.md`：
Go 的 `plugin` 平台与构建耦合；要插件化只能"独立进程 + 协议"，当前体量不值得。
工具可用性只由 `profiles.json` 的 `tools` 决定（单一真相，避免两处开关冲突）。


两层划分：

| 层 | 工具 | 定义位置 | 生效方式 |
|---|---|---|---|
| 内置 | `bash` / `screenshot` / `recall` / `view_image` / `spawn_task` | `internal/tools/tools.go`（Spec） | 编译期 |
| 外置插件 | `kb_search` / `kb_get` / `web_search` / `web_fetch`（以及以后新增） | `~/.data/agentd/tools.d/*.json` + 脚本 | 每次请求重建 registry，改完即生效 |

为什么不走 Go `plugin`：平台与构建参数耦合、无法热重载；我们只需要"独立进程 + 协议"。

插件调用约定（一次性）：

```bash
# 清单 command 直接 exec（不经 shell，避免注入）
# 参数以环境变量传入，脚本无需解析 JSON
AGENTD_ARG_QUERY="agentd" ./scripts/tools/kb_search
# 也可从 stdin 读 {"tool":"…","args":{…}}
```
```
· 参数：AGENTD_ARG_<NAME>（数组用换行连接）；必填参数在宿主侧先校验，缺失直接返回 [error]
· 输出：纯文本，或 {"text":…,"images":[…]}（与内置工具的 content parts 对齐）
· 超时：清单 timeoutMs（默认 20s，上限 10min）；非 0 退出 → 文本 + [exit N]
```

常驻插件（`"persistent": true`）：JSON-RPC 2.0 over stdio，一个进程可暴露多个工具（与 MCP 同构）。

```
initialize → tools/list → tools/call
进程挂了下次调用自动重启；agentd 退出时 ShutdownPlugins()
```

可用性仍由 `profiles.json.tools` 决定（不引入第二套开关）；插件改名/删除不影响脚本文件。

## 5. 上下文：token 口径，字符换算

```
配置与展示用 token；内部裁剪用字符（1 token ≈ 1.8 字符，中英混合经验值）
```
```
两个阈值是同一个数：compact_pct（默认 80%）
  · 触发前：maybeSummarize 生成摘要（模型写，保留 keep_messages 条最近消息）
  · 触发后：compact 才裁剪（切点只要求"不落在 tool 消息上"，避免拆散 tool_call/结果）
全局上限已移除：每个 profile 自己写 max_prompt_tokens / context_window / compact_pct
窗口优先读上游 /v1/models 的 meta.n_ctx，兜底 131072
```

这条设计是被两次事故逼出来的（详见 REPAIR.md）：裁剪阈值与摘要阈值不一致 → 只裁不摘 →
模型"忘记原始问题"，于是反复 recall/重查，最后跑飞；另外字符/字节混用让 token 估算虚高 1.7 倍。

## 6. 生成侧的两道保险

```
max_tokens_out（默认 8192）：单轮输出上限，透传上游 max_tokens
reasoning_budget（默认 4096）：单轮思考预算，透传上游 reasoning_budget_tokens
```
上游默认"思考不限"，没有这两项时出现过单轮思考 6 万 token、GPU 连续 11 分钟的运行。
注意：上游引擎不支持中途 abort，停止后那一段仍要跑完（≤ 上限），需要更硬的终止只能重启引擎。

## 7. 流式与连接

```
/api/chat 与 /api/conv/<id>/stream 都走 SSE：
  · 服务端每 5 秒发 ": ping"（长工具执行期间连接不被判空闲）
  · 事件：meta / reasoning / delta / tool / interject / usage / error / done
  · 前端刷新后用 /stream?from=0 续传；断流时按 /api/running 判断"重连 or 收尾"
插话（interject）：只在"工具轮次开始前"注入；本轮无工具轮次时，结束时落库成 user 消息
```

## 8. 设置是分层的卡片，不是表单

层级固定为「三级 → 二级 → 一级」，同一时刻只有一条路径展开：

```
三级（编辑）：system 提示 / 工具说明
二级（对象）：模型详情 / 工具配置（互斥，点哪个开哪个）
一级（总览）：常规配置 / 模型列表 / 工具列表
```
```
· 窄屏（<1400px）：只显示当前层（.is-top），其余 display:none；× 回到上一层
· 宽屏：三层并排（各占一列），点卡片下方空白或遮罩关闭
· 详情类卡片（模型/工具）共用 .card / .card-hd / .card-bd / .ef-label / .title-actions
  —— 工具卡曾经挂在 .settings 下（不在 .set-cols 里），导致排版和层级全错；现在统一进容器
· 工具来源只在"内置"上打徽标；外置插件不加额外标记
· 三/级卡片的正文（提示编辑）随内容增高，上限为容器可视高度的 100%，超出框内滚动

每一层（列表 / 详情 / 编辑器）都是独立卡片，宽度固定、高度随内容、上限等于视口；
保存/取消只在有改动时出现；遮罩点击逐级退出。理由：配置项会越加越多，
"一张大表单"必然演变成难以维护的条件渲染。

## 9. 云端接入（2026-10-29）

```
同一套 profile 字段接云端：base_url + api_key + model；provider=openai 表示"通用 OpenAI 兼容"
差异点只有两处，都在 provider 里处理：
  · reasoning_budget_tokens 是 Strata 扩展 → openai 时不发（标准 effort 仍可发）
  · 窗口：云端 /v1/models 常把 context_window / context_length 放顶层 → 解析时一并读
实测（DeepSeek）：流式、tool_calls、reasoning_content、usage(cached)、图片输入都兼容；
未知字段 reasoning_budget_tokens 它不报错，但 OpenAI 官方接口会严格校验，所以默认不发。
注意：工具 schema 里 required 必须是数组（不能是 null），否则 OpenAI 严格模式直接 400。
```

## 10. 时间语义只有一个：最后活跃
`updated_at` 是唯一驱动排序与自动归档的时间；`created_at` 只用于审计。
自动归档 = 启动时 + 每小时巡检 + 改动相关设置时立刻巡检，阈值默认 7 天。

## 11. 可观测性优先，但别过度
自建 `internal/logx`：起始、会话归档/恢复、每轮对话、每次工具调用（名称/耗时/输出长度）、
自动归档数量。日志是**给人复盘**用的，不进模型上下文；模型侧的"工具输出"另有截断策略。

## 12. 明确不做的事
- 多用户/鉴权：只监听 127.0.0.1。
- 云端同步、遥测、自动更新。
- 用配置表达分支逻辑：配置只声明"是什么/怎么调"，逻辑留在代码或（将来的）工具进程里。

## 13. 仓库约定
- 远端：`git@github.com:mhqdz/agentd.git`（用 SSH；本机 `~/.ssh/id_ed25519_kb` 已授权）。
- 入库：源码、`docs/`、`web/src`、`web/public/fonts`（字体随项目分发）。
- 不入库（`.gitignore`）：`web/dist/`、`node_modules/`、`*.log`、`agentd`、`/tmp/`。
  - 原因：`dist` 是构建产物；生产服务直接读磁盘上的 `web/dist`，部署前必须 `npm run build`。
- 部署：一律用 `scripts/deploy.sh`（`--no-front` 只发后端）。它会构建到临时文件、替换二进制、
  重启并做 `/api/health` 检查；失败自动打印 `systemctl status` 并退出非 0。
  手动等价命令仅在脚本不可用时使用（见 `PLAN.md` 第三节）。
- 历史里的 `web/dist` 仍在旧提交中（`.git` 约 81MB）；若需瘦身要重写历史，属一次性操作，先备份再强推。

## 14. 系统提示只有一份
提示文本只存在于 `~/.data/agentd/profiles.json` 的 `system` 字段（设置卡里可编辑）。
- 代码里不再硬编码任何提示：`internal/config/profiles.go` 的内置 profile 与
  `internal/config/config.go` 的兜底配置，`System` 一律为空。
- 生效时机：每次 `/api/chat` 现读 profile → 作为 `messages[0]` 的 system 发送；
  改完提示对后续轮次立即生效，历史消息不回改。
- 除提示外，system 里还会拼上「已知事实」（`conversation.kv`）与会话摘要（作为单独 user 消息）。
