# agentd 审计报告

分级依据：P0 崩溃/报错/数据不一致 → P1 卡顿/状态错乱/响应式破坏/同角色样式不统一 → P2 与风格不合时宜/点击目标过小。

每条格式：**现象 → 根因 → 修复 → 验证**。已修的只写验证过的结论，没证据的不下判断（见文末「审计中排除的误报」）。

## 审计环境

| 项 | 值 |
|---|---|
| 后端 | `127.0.0.1:18400`（systemd user unit `agentd`） |
| 数据库 | `~/.data/agentd/agentd.db`（WAL） |
| 浏览器 | geckodriver 4444 + Firefox headless |
| 视觉验证 | `scripts/ui-test/pixel_diff.py`，6 张固定页面/尺寸 |
| 基线 | `~/.data/agentd/pixel-base/`（不放 `/tmp`；每次**刻意**改视觉后重拍，其余改动必须逐像素不变） |

审计起点的自检：对**未改动的 HEAD 构建**跑一次 `pixel_diff compare`，6/6 逐像素一致，证明截图链路确定性足够、基线有效。此后任何视觉提交都必须保持「除刻意改动的那几张外，其余逐像素不变」。

> 文中的 `theme.css:NNN` 是**改动当时**的行号。第 8 批把 7 个 `:root` 合并成一个之后整体前移了 26 行，现在对不上 —— 按选择器找，别按行号。

---

## P0 — 崩溃 / 报错 / 数据不一致

### P0-1 测试脚本往真实会话写数据 ✅ 已修

**现象**：真实对话 `conv=118` 的末尾出现 6 条不该有的消息 —— `No.148 / 150 / 152` 是内容为 `rb-test` 的用户消息，`No.149 / 151 / 153` 是内容为 `（已停止）` 的 assistant 消息。用户从没发过这些。

**根因**：`scripts/ui-test/robustness.py` 把真实会话当测试靶子：

- `[3] 超长输入`（第 52 行）：`s.get(BASE + "/?conv=118")`，然后往 textarea 灌 10000 个 `x`
- `[4] 快速连点发送`（第 67 行）：同样打开 `?conv=118`，填 `rb-test`，**真的点 5 次发送按钮**

所以每跑一次 robustness 就多一轮真实消息，还要调用一次真实模型。跑过 3 次 → 6 条垃圾。

同类问题还有一处：`[4]` 收尾停任务时不看 conv_id，`for cid in running: stop` —— 会顺手停掉用户正在跑的那一轮。

**修复**：

- 新增 `new_conv()`，用 SQLite 自建一个会话当靶子（会话表结构简单，且测试脚本本来就有写库权限）；`[3][4]` 全部改用自建会话，不再出现硬编码的真实 conv id
- 停止逻辑加 `if str(cid) != rb_conv: continue`，只停自己这一轮
- 新增 `dispose()`，跑完把自建会话连同消息真删（原先用 `DELETE /api/conv/<id>`，那只是 `archived=1`，仍占会话列表位）
- `stream_test.py` 同样补上收尾清理，同样从「归档」改为「删」

**验证**：

- 清理前备份：`agentd.db.bak-audit-20261103-035417`
- 删除 `conv=118` 的 `seq 148..153`：消息数 153 → 147，`max(seq)` 153 → 147
- 全库 `rb-test` 分布复查：只剩 `conv=118` 的 3 条，删除后归零
- 重跑 `robustness.py`：0 个问题，且 `conv=118` 残留检查输出 `0`
- 清理全部测试会话（`stream-test` / `uitest` / `ui-rb`）：会话总数 29 → 23

### P0-2 传了不存在的会话 id 会静默新建 ✅ 已修

**现象**：`POST /api/chat` 带 `{"conv_id":"999999","text":"hi"}`，返回的不是报错，而是一个**新会话**的 SSE 帧（`{"conv_id":"173","title":"新会话"}`），并且真的跑完了一轮，往新会话里写了 2 条消息。

**根因**：`Agent.Conversation(id)` 的实现是「读不到就新建」：

```go
if id != "" {
    if c, err := a.st.Get(id); err == nil {
        return c, nil
    }
}
// 读不到 → 掉到这里，静默新建
return &store.Conversation{ID: a.st.NextID(), Title: "新会话", ...}, nil
```

而且它**永远返回 nil 错误**，所以 `handleChat` 里那段 `if err != nil { send("error", ...) }` 是死代码 —— 从来没执行过。

**后果**（数据不一致）：前端把 conv_id 拼错时，消息会悄悄写进一个新会话，用户以为还在原会话；每一次这样的请求还会真实调用一次模型（白烧）。

**修复**：区分三种输入 —— 空 id 才是新建；传了 id 但读不到必须报错。

**验证**：
- 单元测试 `TestConversationResolution`：空 id 新建、存在 id 原样返回、带首尾空白的 id 能匹配、不存在的 id 报错**且不产生新会话**
- 部署后实测：`{"conv_id":"999999"}` → `event: error` + `{"error":"会话 \"999999\" 不存在或读不出来：file does not exist"}`，会话数保持 21
- `go test ./...` 全绿

**顺带**：CLI 的 `agentd ask -conv <id>` 现在会 `fatal` 而不是静默新开 —— 它本来就处理了 err 分支，只是从前永远拿不到错误。

### P0-3 `/shots/` 目录遍历：整个数据库可下载 ✅ 已修

**这是本轮最严重的发现。**

**现象**：

```
GET /shots/..%2Fagentd.db  →  200  20586496 bytes
```

20MB，正是 `~/.data/agentd/agentd.db` —— 全部 1714 条对话消息、会话元数据、配置。同样的手法能拿到 `ui.json`，再编码几层理论上还能走到上一级目录。

**根因**：两个问题叠在一起。

```go
mux.HandleFunc("/shots/", func(w http.ResponseWriter, r *http.Request) {
    home, _ := os.UserHomeDir()
    http.FileServer(http.Dir(filepath.Join(home, ".data", "agentd"))).ServeHTTP(w, r)
})
```

1. **`FileServer` 的根是 `~/.data/agentd` 而不是 `shots/`。** 因为 URL 前缀也得参与映射，`/shots/x.png` 落到 `~/.data/agentd/shots/x.png` —— 看起来"刚好对"，实则把整个父目录（套着数据库和配置文件）都暴露了。
2. **`http.Dir` 的越界防护挡不住 URL 编码。** 它用 `path.Clean("/"+name)` 做清理，但清理发生在**解码之前**：`..%2F` 在 Clean 眼里只是一个普通片段，Clean 之后才解码成 `../`。

**修复**：先把前缀剥掉，再让 FileServer 的根直接落在 `shots/` 上：

```go
dir := http.Dir(filepath.Join(home, ".data", "agentd", "shots"))
http.StripPrefix("/shots/", http.FileServer(dir)).ServeHTTP(w, r)
```

这样 `..` 无论怎么编码，Clean 之后都还在 `shots` 之下。

**验证**：
- 越界 `/shots/..%2Fagentd.db`、`/shots/..%2Fui.json`、`/shots/..%2F..%2F.ssh%2Fid_rsa` → 全部 **404**
- 正常截图 `/shots/1793375166786760459.png` → **200**，2.5MB 正常返回
- `/shots/../agentd.db` 会 307 到 `/agentd.db`，那个路径由 `handleStatic` 处理 → 404
- 三条越界用例已固化进 `check_api.py`，34 条全过

**教训**：这个洞一直在，只是没人试过编码后的 `..`。前面的审计都在看"功能对不对"，没看"能不能被滥用" —— 而 `http.FileServer` + 手拼 `http.Dir` 是最容易踩的一类。

### P0-4 跨站请求伪造：任意网页都能让 agentd 跑一轮 ✅ 已修

**现象**：

```
POST /api/chat   Content-Type: text/plain
{"conv_id":"207","text":"只回复两个字：好的"}
→ 200  event: meta   会话里落了 2 条消息
```

后端只读 body、不看 `Content-Type`。而 `text/plain` 属于 CORS 的**简单请求**，浏览器**不触发预检**，请求照常送达。

**攻击成本**：随便一个网页里一行 `fetch` 就够。响应因为没给 CORS 头读不到，但**副作用已经发生** —— 这一轮真的会跑，而模型手上是 `bash` 工具。

**根因**：服务端没有任何 CSRF 防护，`handleChat` 直接 `json.NewDecoder(r.Body).Decode(...)`。之前"看起来还好"是因为表单的 `enctype=text/plain` 会拼成 `key=value` 而不是纯 JSON，解析失败；但 `fetch` 能直接发纯 JSON。

**修复**：加 `requireJSONBody` 中间件 —— 所有带 body 的 POST/PUT/PATCH 必须声明 `application/json`，否则 415。浏览器发这个类型**必然先发 OPTIONS 预检**，而不给 CORS 头的服务端不会放行预检。用 `ContentLength != 0`（而非 `> 0`）把 chunked 的 -1 也一起要求；无 body 的 POST（停止生成）照旧放行。

**验证**：
- `text/plain` + JSON body → **415**
- `application/json` → 正常
- 无 body 的 `POST /api/conv/51/stop` → 404（"这一轮已经结束"，不是被中间件拦）
- `check_api` 34 条 + `check_live` / `check_sendstop` / `stream_test` / `robustness` / `run*` 全绿
- 5 处 Go 测试的请求补了 Content-Type（真客户端都带，是测试漏了）

### P2-10 没有任何安全响应头 ✅ 已修

**现象**：`X-Frame-Options` / `X-Content-Type-Options` / `Referrer-Policy` 一个都没有。

**风险**：
- 没有 `X-Frame-Options`：任意页面能把界面 iframe 进去做点击劫持（透明浮层盖在「删除插件」上）
- 没有 `nosniff`：浏览器可能把 JSON 响应猜成 HTML 去执行
- 没有 `Referrer-Policy`：URL 里带 conv id，会随外链带出去

**修复**：加 `secureHeaders` 中间件，三个头一起给。**刻意不加 CORS 头** —— 跨站读不到任何响应，这正是想要的。

**验证**：`curl -I` 三个头都在；用 JS 建 iframe 加载同源页面，`contentDocument` 为 `null`（被 DENY 拒绝）。

### P1-12 `/api/wake` 接受不存在的会话，写入孤儿记录 ✅ 已修

**现象**：带正确 token 调 `POST /api/wake {"conv_id":"999999","text":"孤儿测试"}` → `{"ok":true,"outbox_id":18}`。会话根本不存在，outbox 里却真的多了一条。

**根因**：`handleWake` 校验了 token、也校验了 `conv_id` 是正整数，但**没校验会话存在**：

```go
conv, _ := strconv.ParseInt(in.ConvID, 10, 64)          // 只要 > 0 就过
id, err := s.hst.CreateOutbox(conv, in.Origin, in.Text) // 不查存在性
```

`CreateOutbox` 不做外键检查（SQLite 默认也不强制），写进去的就是一条**永远没人消费**的记录 —— 没有对应会话的 agent 会去读它。

**怎么发现的**：上一轮把 `Conversation()` 改成「读不到就报错」之后，顺着同一条线索去查其它接受 id 的写入口。`/api/wake` 是唯一带 token 的端点，也是唯一一个我还没测过「不存在 id」的写入口。

**修复**：`CreateOutbox` 之前先 `s.st.Get(in.ConvID)`，读不到回 404。

**验证**：`conv_id: 999999` → `404 {"error":"没有这个会话"}`；真实会话 → `200 {"ok":true,"outbox_id":19}`。

**顺带清理了两条历史孤儿**：`id=17` 指向 conv 99（早先删掉的测试会话 `uitest`），`id=18` 是这次造的。两条已删，outbox 现在为空。

**这里要记一笔自己的失误**：验证 200 那条时我直接用了 `conv_id=51`（真实会话），outbox 被消费后在里面多出一条 `（测试写一条）`（seq 576）。已删除并确认回到 575 条、最后一条仍是原来的 `seq 575`。**同一个错误（拿真实会话当靶子）我在这轮里犯了第二次** —— 上一个是 robustness.py 写脏 conv118。以后写这个端点的验证必须走 `dbtool.new_conv()`。


### P1-13 `/api/tasks` 派发到不存在的父会话 ✅ 已修（与 P1-12 同一个模式）

**现象**：`POST /api/tasks {"kind":"smoke","conv_id":"999999"}` → `{"ok":true,"task_id":16}`，库里多出一条 `status=running` 的任务，父会话是 999999。

**根因**：和 `/api/wake` 一模一样 —— 只校验 `conv_id` 是正整数，不校验它指向的会话存在。差别是后果更显眼：`running` 状态永远挂着，`/api/tasks` 的 `active` 计数会一直显示有个任务在跑。

**修复**：`Dispatch` 之前先 `s.st.Get(in.ConvID)`，读不到回 404。

**验证**：999999 → 404；清理后 tasks 表归零。

**同类排查完毕**：接受 id 的写入口一共四个 —— `Conversation()`（P0-2）、`/api/conv/<id>/interject`（P1-10）、`/api/wake`（P1-12）、`/api/tasks`（这条）。前两个修过，后两个这轮补上。读入口（`GET /api/conv/<id>`、`/api/tasks/<id>`、`/api/profiles/<name>`）本来就都有 404。

### 测试脚手架：`run2.py` 依赖库里已有的任务

**现象**：清空 tasks 表后跑 `run2.py`，第 22 行「点任务行展开步骤」`0->0` 失败，第 32 行直接 `TypeError: 'NoneType' object is not subscriptable` —— 它写死了 `json['tasks'][0]['id']`。

**意义**：这不是产品 bug，但它会让「全新环境」或「刚清过数据」的状态下一跑就崩，而崩的原因看起来像是功能坏了。测试套件本该在任何干净状态下都能跑。

**修复**：
- 开头用 `dbtool.new_conv()` 建会话、派一条 `smoke` 任务、轮询等步骤出现
- 深链用例改用自己拿到的 `task_id`，不再取 `tasks[0]`
- 收尾删 `task_steps` / `tasks` / **`outbox`**（smoke 跑完会写一条「任务回报」，父会话删了它就是孤儿，第一版清理漏了这个，跑一次 outbox 就从 0 涨到 3）
- 顺带修掉结尾重复的 `s.close()`

**验证**：跑前 `会话21 / 任务0 / outbox0`，跑完打印「已清理测试任务与会话 19 238」，再看仍是 `21 / 0 / 0`。

### 测试脚手架：`run.py` 造的会话和任务从不清理

**现象**：跑完完整回归后 `pixel_diff` 突然大面积失败 —— `wide-conv51` 269143 像素、`wide-settings` 232816、`narrow-conv51` 168240，而这轮我一行前端代码都没改。

**根因**：`run.py` 在 tasks 表为空时会造一个 `uitest` 会话 + 一条 smoke 任务，**结尾不清理**。库里于是多出一个会话、一条任务、一条 outbox，`wide-empty` 页面上「会话 N」这个数字就变了。

而且 `run2.py` 上一轮已经改成自建任务并清理，`run.py` 没跟上 —— 同一个测试套件里两套规矩，迟早会咬人。

**修复**：`run.py` 改用 `dbtool.new_conv()`、记录自己造的 `task_id`、结尾删 `task_steps` / `tasks` / **`outbox`** 再删会话。**只清理自己造的那份** —— 库里本来就有任务时那是别人的，不动。

**验证**：跑前 `21 / 0 / 0`，跑完打印「已清理测试任务与会话 22 249」，再看仍是 `21 / 0 / 0`；`pixel_diff` 恢复 6/6。

### 一次连锁反应（值得记）

清理 conv51 时发现上面多出的 5 条消息（seq 576-580）不是我直接写的，而是**我的 wake 测试间接引发的**：

1. 我用 `conv_id=51` 测 `/api/wake`，写了一条 outbox
2. 那条 outbox 被正常消费，模型收到「（测试写一条）」
3. 模型按它的理解去查 `kb` 写入规范，连跑了几轮工具调用，留下 576-580

我只删了 576（那条 user 消息），没意识到后面还有模型自己跑出来的 4 条。直到 `pixel_diff` 崩了才顺藤摸瓜找回来。

**两个教训**：一是**写测试永远用自建会话**（这个错误我这轮犯了两次）；二是清理数据时要**检查连锁产物**，删掉一条消息不代表它引发的后续也消失了。

### P1-14 每轮都报「压缩中…（上下文超阈值）」✅ 已修

**现象**：一条全新的会话（上下文总共二十来个字符），发消息时 SSE 里照样有：

```
event: stage
data: {"stage":"compacting"}
```

前端据此显示「压缩中…（上下文超阈值，正在写摘要）」—— 对一个刚建的会话，这句话完全是假的。

**根因**：

```go
if a.OnStage != nil {
    a.OnStage("compacting")          // 无条件发
}
_ = a.maybeSummarize(ctx, c, keep)   // 它自己内部才判阈值
```

`maybeSummarize` 第一件事就是 `if total < a.compactAt() { return nil }`，绝大多数轮次直接返回。阶段事件却在它之前就发出去了。

**为什么值得修**：这是**错误信息**，而且出现在最不该出现的地方 —— 用户刚开口，界面就告诉他「上下文超阈值」。它通常一闪而过（`generating` 紧随其后），但恰恰是这种「一闪而过的假话」最消耗信任。

**修复**：抽出 `willSummarize(c)`，条件与 `maybeSummarize` 的入口保持一致（总字符数 ≥ `compactAt()`），只有成立时才发阶段事件。

**验证**：在隔离实例（`AGENTD_DATA_DIR=/tmp/agentd-fresh`、端口 18500）上，新会话的 stage 序列从 `compacting, generating, generating` 变成 `generating, generating`；真超阈值的会话本来就会进压缩分支，不受影响。

### 首次走查「新装 → 第一次对话」（结果：正常）

以前所有验证都在有数据的实例上做，从没试过全新环境。这轮起了个彻底隔离的实例（独立数据目录 + 独立端口 + 独立进程），从空目录走了一遍：

| 步骤 | 结果 |
|---|---|
| `agentd serve` 首启 | 自动建库（`agentd.db` + WAL）、建 `handoff/` 目录 |
| `GET /api/health` | 200，返回 base_url / model |
| `GET /api/profiles` | 内置 `180-low` 等默认 profile 已在 |
| `GET /api/convs` | `[]` |
| 第一次 `POST /api/chat`（不带 conv_id） | 建会话、标题取首条消息、user + assistant 两条落库 |

没有卡住的地方。隔离实例验证完已停掉、临时目录已删，主实例数据不受影响（21 个会话）。

### P1-15 `AGENTD_DATA_DIR` 只隔离了数据库 ✅ 已修

**现象**：为了走查「新装 → 第一次对话」，我起了一个隔离实例：

```
AGENTD_DATA_DIR=/tmp/agentd-fresh  AGENTD_ADDR=127.0.0.1:18500
```

数据库确实建在 `/tmp/agentd-fresh`，但**它连上了真实模型** —— 我把 `AGENTD_BASE_URL` 指到一个不存在的端口（19999），回复照样正常。

**根因**：`AGENTD_DATA_DIR` 只被数据库用到。其余本地数据全硬编码在 `~/.data/agentd` 下：

| 文件 | 实际位置 |
|---|---|
| `profiles.json` | `~/.data/agentd/`（只能由 `AGENTD_PROFILES` 单独覆盖） |
| `settings.json` | 同上 |
| `wake.token` | 同上 |
| `tools.json` / `tools.d/` | 同上 |
| `tasks.d/` | 同上 |
| `shots/` / `out/` | 同上 |

隔离实例读的是**主实例的 profiles**，其中 `180-low` 的 `base_url` 是 `127.0.0.1:18320`，而 profile 的 `base_url` 优先级高于 `AGENTD_BASE_URL` —— 于是「隔离实例」连的是真模型。

**影响**：
- 想跑多实例（不同数据）的人会踩：库是新的，配置和插件还是那份
- 测试没法真正隔离（我这轮就踩了，第一次「错误路径」测试其实在打真模型）

**修复**：`internal/config` 加 `DataDir()` / `DataPath()`，认 `AGENTD_DATA_DIR`，默认 `~/.data/agentd`。把 **13 处硬编码**全换掉，横跨 config / tools / handoff / web / agent 五个包。`AGENTD_PROFILES` 保留（显式覆盖优先）。

**验证**：
- 主实例重启后：会话 21、conv51 575 条，`profiles.json` / `settings.json` / `tools.json` 仍在原位，一切照旧
- 隔离实例第一次失败：没给 `AGENTD_PROFILES` 时 `LoadProfiles()` 回落到**内置默认**（仍指向 18320），还是连了真模型 —— 顺便说明内置默认 profile 本身就是个隐藏的隔离漏洞
- 显式给了 `AGENTD_PROFILES` 之后才真正断开：`POST /api/chat` 的事件序列是 `meta → stage → stage → error → usage`，会话落库为 `user(done)` + `assistant(failed)`，内容是 `（中断：Post "http://127.0.0.1:19999/nope/v1/chat/completions": connection refused）`

**顺带确认了错误路径是完整的**：模型不可达时前端能收到 `error` 事件、消息以 `failed` 状态落库、刷新后仍在。这条路径以前没验证过。

### 真压缩路径（实测：正常）

隔离实例上造了一个 80 条消息、**204,422 字符**的会话（阈值按 `131072 × 1.8 × 0.8` 估是 188,743），发一句话触发压缩：

```
[INFO] compact conv=1 msgs=81->31 chars=204430->71611 budget=151200 keep=16
```

- `compacting` 阶段这次是真的出现了（P1-14 修完之后，只有真压缩才发）
- 摘要 395 字符落库，`summary_upto=66`、`summary_model` / `summary_at` 都写了
- 数据库里消息**没被删**（80 → 82，历史完整），只裁掉发给模型的那部分 —— 这是正确设计
- 分页侧一致：`total_rounds=41`、`from=32`、`remaining_rounds=31`

顺带发现实际 `budget` 是 151200，对应 `maxChars` 189,000（≈105,000 tokens），说明 `ContextWindow()` 探测到的是 105000 而不是回退默认的 131072 —— 探测链路是好的。

### 长会话上翻分页（实测：正常）

conv51（575 条、41 轮）连续三次滚到顶部：

| 次数 | `.msg` 数 | 首条 | 滚动位置 |
|---|---|---|---|
| 初始 | 99 | `No.362` | 31549 |
| 上翻 1 | 192 | `No.051` | 21689 |
| 上翻 2 | 204 | `No.001` | 6525 |
| 上翻 3 | 204 | `No.001` | 0 |

每次 +10 轮左右，到顶后不再变（正确拒绝继续加载），而且**加载后滚动位置保持了**（不是跳回顶部）—— 这是上翻分页最容易做错的地方。

### 测试数据的时间格式（脚手架）

`dbtool.new_conv()` 原来用 `time.strftime("%Y-%m-%dT%H:%M:%S")`，少了时区。Go 侧按 RFC3339 解析会失败、拿到零值（`0001-01-01T00:00:00Z`）。

会话本身不读 `created_at`（只有任务列表读），所以一直没暴露；但造出来的数据不像真的。改成 `datetime.now().astimezone().isoformat()`，实测落库为 `2026-11-03T06:38:17.514012+08:00`，和真实数据一致。

### P0-5 客户端一断开，服务端就 panic ✅ 已修

**这是本轮最严重的一个。**

**怎么发现的**：为了测并发，我让 curl 带着 90 秒超时发请求、同时起两个。跑完发现：

- `/api/running` 是空的
- 但库里留着一条 `status=running` 的僵尸消息
- 请求方的 SSE 停在 `event: delta` 就没有下文了
- 服务日志里有一大段 **panic 堆栈**

**堆栈指向的地方**：

```
bufio.(*Writer).Flush(...)
net/http.(*response).FlushError(...)
net/http.(*response).Flush(...)
agentd/internal/web.(*Server).handleChat.func1(...)   web.go:755
```

也就是 `send()` 里那句 `flusher.Flush()`。客户端一断开（curl 超时、浏览器刷新、关标签页），底层 `bufio` 拿到 `ErrShortWrite` 就 panic。panic 杀掉这个请求的 goroutine —— 于是：

- `chat.done` 永远不会打印
- 这一轮预分配的 seq 留在库里、永远是 `running`
- 也就是那条僵尸消息的来历

**影响面比想象大**：任何一次页面刷新都可能踩到（SSE 断开但服务端还在跑）。只是平时没人去看日志。

**修复**：两处 `Flush` 都换成 `http.NewResponseController(w).Flush()` —— 它返回 error 而不是 panic（Go 1.20+）。写失败就直接 return，后面的帧不再写；心跳同理。

**验证**：`--max-time 3` 让客户端在 3 秒后断开，等 12 秒再看：

- 日志里 panic 计数 **0**
- 消息正常落库：`user(done, 14 字符)` + `assistant(done, 197 字符)`
- `chat.done conv=1 secs=4.2` 正常打印

也就是说：客户端断开不再中断这一轮，服务端照跑完、照落库 —— 这本来就是这个设计（`context.Background()` 不绑请求），只是以前被 panic 打断了。

**补一刀（同一批修复的遗漏）**：上一批只改了 `web.go` 里的两处 `Flush`，**漏了 `job.go` 里的 4 处**。`streamFrom` 是断点续传那条路径 —— 浏览器订阅 `?from=N` 后跟随后续帧，用户刷新/关标签页时同样会断开，所以那里一样会 panic。

这批把 `job.go` 的四处置换掉（回 done 流、写帧循环、收尾 done、心跳），并顺手给两处 `fmt.Fprint` 补了写错误检查。

**验证**：订阅 `?from=0` 后 0.4 秒强制断开，连做三次，日志 panic 计数 **0**，消息仍完整落库（`user/done` → `assistant/done` → `tool/done` → `assistant/done, 345 字符`），`chat.done secs=7.4` 正常。

**教训**：修 panic 这类问题必须**全仓库搜一遍同一个调用模式**（`grep -n "flusher.Flush()"`），而不是只改报错栈里出现的那一处。我第一次修完就应该跑这个 grep。

### 分页窗口的边界（新增 `check_paging.py`，16 条断言全过）

以前分页只在 conv51（575 条、41 轮）上测过 —— 那种规模下 `first < 0` 之类的 clamp 根本不会触发。这轮专门造了空会话、1 轮、2 轮三种极小样本，把参数边界都打了一遍：

| 用例 | 结果 |
|---|---|
| 空会话 | `from=0 to=0 total_rounds=0`，不报错 |
| 1 轮会话（2 条消息） | `from=1 to=1`，`has_more=false` |
| 3 条消息 | 正确算出 `total_rounds=2`，全量返回 |
| `before=1`（小于最小 seq） | 空窗口 `from=0 to=0`，`has_more=false` |
| `around=99`（超过总轮数） | 夹回 `from=1 to=2` |
| `around=0` | 当作未指定 |
| `rounds=0` / `rounds=-5` | 回落默认 10，**不产生负窗口** |

除了逐项数值，还写了个 `sane(p)` 检查一组不变量：`from/to` 非负、`from<=to`、`to<=total_rounds`、`remaining_rounds` 与 `total_rounds` 自洽、窗口不超过 `rounds`。所有用例都通过。

**结论**：分页的边界处理是干净的，没有需要修的。但这个脚本留下了 —— 这些值直接决定标尺画几格、上翻还能不能再加载，算错就是"状态错乱"，以后改分页有一道网。

### 极端标题的渲染（实测：正常）

按「标题只显示用户原话、不压正文」这条已确认的决策，造了三种极端标题：

- 24 个 emoji（`🚀🎉🔥…`）
- 44 个字符、完全没有空格的中文长句
- 60 字符的超长 URL

量 `.ef-record` 的 `scrollWidth - clientWidth`：三者都是 **0**，宽度稳定在 221px。emoji 标题的 `textContent.length` 是 83（代理对让 length 翻倍），但 `text-overflow: ellipsis` 照常截断，没有撑破、也没有压到正文。

### P1-18 轮次多时右侧标尺溢出视口，大半刻度点不到 ✅ 已修

**现象**：造了一个 200 轮的会话（400 条消息）打开，右侧标尺渲染了 200 条刻度：

```
marks: 200   h: 2789px   top: -937   bottom: 1851   winH: 914
```

**上下各溢出 937px**。刻度是 `position: absolute` 居中的，溢出部分直接被裁到视口外 —— 也就是说 200 轮里有 140 多轮**根本没有可点的刻度**，标尺从"点任意一轮跳过去"退化成"只能点中间那一小段"。

**根因**：`.scale` 用的是固定 `gap: 12px`，而刻度数量等于 `page.total_rounds`（无上限）：

```jsx
Array.from({ length: total || 1 }).map(...)   // 200 轮 → 200 条
```

```css
.scale { display:flex; flex-direction:column; gap:12px }
```

200 × (2px 高 + 12px 间距) = 2800px，必然超出 914px 的视口。

**为什么常规测试没发现**：conv51 是 41 轮，41 × 14 = 574px，稳稳装得下。要 60 轮以上才会开始装不下 —— 而真实会话里目前最长的就是 41 轮。

**修复**：让间距自适应，取「12px」和「刚好装下」的较小值：

```js
const usable = window.innerHeight - 48
const g = Math.floor((usable - n * 2) / (n - 1))
scaleGap = Math.max(1, Math.min(12, g)) + 'px'
```

CSS 改成 `gap: var(--gap, 12px)`。

**为什么这样不破坏已确认的视觉**：算一下就知道阈值在 61 轮 —— `(866 - 2n)/(n-1) >= 12` 当且仅当 `n <= 61`。所以 **60 轮以内 gap 仍是 12px，一像素都不变**。

**验证**：
- 200 轮：`gap: 2px`，`h: 799`、`top: 58`、`bottom: 856`，**上下溢出都是 0**
- 41 轮：`gap` 仍是 `12px`，`h: 311`，和改动前一样
- `pixel_diff` 6/6 逐像素一致（`wide-empty` 中途报过一次 79 像素差异，查出来是我那个 200 轮测试会话没删、会话计数变了，删掉即恢复）

**回归用例**：新增 `check_scale.py` —— 造一个 200 轮会话，断言刻度渲染出来、总高不超视口、上下都不溢出、间距确实被压缩；再打开 41 轮的 conv51 断言 `gap` 仍是 `12px`（确认没把常见会话的视觉改掉）。7 条断言全过。

这个用例值得留下的原因：间距是"算出来的"，将来调 `--gap` 的计算公式或 `.scale` 的布局，很容易在某个轮次区间又溢出 —— 而那时 41 轮的对比图仍然会显示"一致"。

### P1-19 子会话的 `↳#N` 编号在 id 跨位数后错乱 ✅ 已修

**现象**：造一个父会话，下面挂四个子会话（id 998 / 999 / 1000 / 1001），接口返回的序号是：

| id | Seq |
|---|---|
| 998 | 3 |
| 999 | 4 |
| **1000** | **1** |
| **1001** | **2** |

后建的两个反而排在前面。

**根因**：

```go
sort.Slice(kids, func(i, j int) bool { return kids[i].ID < kids[j].ID })
```

`ID` 是 `string`，这里是**字符串比较**。id 来自 SQLite 自增列，跨位数之后 `"1000" < "999"` 就成立了。于是服务端按字典序编号，左栏的 `↳#1` 指向了最新的子会话，而不是第一个。

**为什么现在才发现**：此前所有子会话 id 都是三位数（99 → 999），同位数下字符串序和数值序一致。而库里当前的 id 正好刚过 1000 —— 也就是说**下一次真实派生子会话就会踩到**。

**修复**：先 `strconv.ParseInt` 按数值比，非数字 id 退回字符串比较兜底。

**验证**：同一组数据，编号变成 `998→1, 999→2, 1000→3, 1001→4`，符合创建顺序。

### 父会话归档后，子会话不会消失（实测：正常）

顺着同一条链路查了个担心的问题：父会话归档后在未归档列表里查不到，那前端的父子分组会不会把子会话一起藏掉？

实测：归档父之前左栏 21 条、子会话可见；归档之后 20 条、**子会话仍可见**。查代码确认有兜底 —— `App.jsx` 里那句注释写着「父不在本列表时按顶层显示」，所以子会话会退化成顶层条目，不会被吃掉。

### handoff 任务的并发与失败恢复（实测：正常）

`Dispatch` 是裸 `go m.run(...)`，没有并发上限 —— 想确认这会不会是个隐患。

**并发 20 个 `smoke`**：20 个请求全 200，最终 20 个 `done`，服务内存 4MB。smoke 只有 `echo`/`sleep`，压不出问题。

**并发 5 个会 `spawn` 子会话的配方**：5 个子会话都建出来了（id 2–6），每个 6–7 条消息、全部 `done`，任务 25 个 `done` / 5 个 `failed`（后者是 5 个 profile 不存在的用例）。内存仍是 4MB 量级。

**结论**：没有硬上限，但在本地模型的量级下不构成问题。**没有加限制** —— 加一个硬编码的并发上限属于"猜用户想怎么用"，真需要时应该做成可配置而不是我现在拍一个数。

**失败恢复也验证了**。造一个配方：`echo before` → `exit 3` → `echo after-should-not-run`，`restore` 是 `echo restored-ok`。结果：

| 步骤 | 结果 |
|---|---|
| `echo before` | `ok` |
| `exit 3` | `failed`（exit_code=3） |
| `echo after-should-not-run` | **没有执行**（正确短路） |
| `restore: echo restored-ok` | `ok` |

任务状态 `failed`，错误信息 `步骤失败（exit 3）：`。**失败短路、后续步骤不跑、restore 照常执行**，这三件事都对。

### P2-11 「插话」按钮没说明它什么时候生效 ✅ 已修

**现象**：在模型生成中途点「插话」并输入「先只回答两个字：停」，落库结果是：

```
1 user      请详细解释一下什么是递归，写五百字
2 assistant （空）
3 tool      ok [exit 0]
4 assistant # 递归（Recursion）…（五百字）
5 user      等一下，先只回答两个字：停   ← 插话，落在最后
```

插话没有打断生成，而是排到了回复之后。

**这本身不是 bug**：`Drain()` 只在工具轮次之间被调用（`loop.go:126` / `181`），前端注释也写着「把内容压进后端队列，模型在下一个工具轮次就能看到（不中断当前生成）」。这次测试里模型直接一口气写完，中间没有工具轮次，所以插话自然只能等下一轮。

**问题在 UI 没把这个说清楚**：按钮只有 `title="插话"`，点完状态栏显示「已插话」。用户会以为模型已经看到并要回应了 —— 而实际上要等到下一轮。

**修复**：只改文案，不动行为（自动触发下一轮属于「猜用户意图」，而且容易做成死循环）：
- `title` → 「插话：不中断当前生成，模型要到下一个工具轮次或下一轮才看到」
- 状态栏 → 「已插话（模型下一轮才会看到）」

**验证**：`pixel_diff` 6/6 一致（`title` 是悬浮提示，不进截图）；`check_live` / `check_sendstop` / `stream_test` / `robustness` / `check_api` / `run` / `run2` 全绿。

### P1-20 文档写的 `/api/tasks/{id}/cancel` 根本没实现 ✅ 已补

**怎么发现的**：这轮换了个方向，不看代码看文档 —— 拿 `docs/HANDOFF.md` 里的端点表逐条打，结果 `/api/tasks/1/cancel` 返回 404。一开始以为是「任务 1 不存在」，读代码才确认 `handleTasks` 的 `switch parts[1]` 只有 `restore` 一个分支，其余全落到 `default: 404 未知操作`。

有意思的是 `run()` 早就把 `cancel` 存进 `m.active[tid]` 了，`Active()` 也在数它 —— **能力是齐的，只是没有出口**。

**补法**：`Manager.Cancel(tid)` 调用已有的 cancel，路由加 `case "cancel"`。顺带验证：造一个 `sleep 30` 的任务，2 秒后取消，返回 `{"canceled":37,"ok":true}`，`sleep 30` 那步变成 `failed` / `exit_code=-1`。

**已知不完美**：任务最终状态是 `failed` 而不是 `canceled` —— 用户主动取消和真的失败在界面上一模一样。要区分得让 `record` 知道这次是取消，改动比这一批大，先记着。

**另外两个文档写了但没实现的**，直接标成未实现而不是硬补：
- `POST /api/tasks/{id}/retry`
- `POST /api/outbox/{id}/deliver|cancel`（连路由都没有）

### P2-12 文档与实现漂移 7 处 ✅ 已修

拿 `docs/API.md` 和 `docs/HANDOFF.md` 的端点表逐条打（能打的全打了），7 处对不上：

| 文档写的 | 实际情况 |
|---|---|
| `POST /api/conv/{id}/archive`（DELETE 同效） | **只有 DELETE**；POST 返回 `405 GET/DELETE only` |
| `stream` 不在运行且无缓冲时 **404** | 返回 **200 + `event: done`**（代码注释还特意写了"不报错"） |
| `PUT /api/profiles/{name}` 新建或更新 | **405**；正确路径是 `POST /api/profiles`（集合） |
| `GET /api/conv/{id}` 返回 7 个字段 | 实际 12 个（多了 `kind/kv/summary/summary_upto/summary_model/summary_at/page`） |
| `POST /api/tasks/{id}/retry` | 未实现 |
| `POST /api/outbox/{id}/deliver\|cancel` | 未实现（无路由） |
| `POST /api/tasks/{id}/cancel` | 未实现 → **这一批补上了**（见 P1-20） |

都按实际行为改正，未实现的明确标 ~~删除线~~ + "未实现"。

**为什么值得修**：文档漂移比代码 bug 更隐蔽 —— 代码有测试兜底，文档没有。这次要不是专门去打，这几处会一直错着，误导下一个读文档的人（包括未来的我）。

**一个自己的小事故**：验证 `PUT /api/profiles/{name}` 时我随手用了 `test-x` / `test-y` 两个名字，结果 `test-y` 真被 `POST /api/profiles` 创建进了主实例的配置。已删除并确认 profiles 列表回到原来的 7 个。

### P2-13 第二批文档漂移 6 处（DESIGN / UI / README）✅ 已修

继续拿文档当测试用例，这轮看的是「设计原则」和「视觉约定」两份。

**最值得说的是 `DESIGN.md` 第 2 节**，它记录的是一个**已经演进掉的设计**：

> "正在生成""当前第几轮""任务缓冲区"只在内存；数据库只存用户可见的事实。
> **中断的判定靠数据（最后一条不是 assistant，或正文带 `——（中断：…）`），不靠状态位。**

而实际代码里：

- `messages.status` **确实落库**，取值 `running` / `done` / `failed` / `interrupted`
- 中断判定用的就是 `status` 位，不是"正文带 `——（中断：…`"
- 为了不让重启后留下僵尸 `running`，启动时会扫一遍把 `running` 改写成 `interrupted` 并打 WARN

所以准确的表述是：**状态位落了库，但只在本次进程内可信**。这不只是措辞问题 —— 照文档理解代码，会去错误的地方找中断标记。

修法不是删掉原设计理由（那个"为什么"仍有价值），而是把演进过程和代价写清楚。

其余五处：

| 文档 | 写的 | 实际 |
|---|---|---|
| `DESIGN.md` | 内置工具 `bash`/`screenshot`/`recall` | 还有 `view_image` / `spawn_task` |
| `DESIGN.md` | SSE 帧进**环形缓冲** | 是按会话保存的 `[]string` + 5 分钟 TTL |
| `UI.md` | 色阶 `--c1`~`--c7` | 还有 `--c8:#637172`（`.no` / `.tt` 用） |
| `UI.md` | `.ef-*` 字体变量"需要同步" | 已同步完毕，改成描述现状 |
| `README.md` | 索引 8 条 | 少了 `AUDIT.md` |

**检查过但没问题的**：`docs/screenshots/` 那 6 张图是 04:27 拍的，之后只改过标尺间距（>61 轮才触发）和插话的 `title` 文案，`pixel_diff` 6/6 一致 —— 图还是准的。

**两轮文档审计合计修了 13 处漂移**。共同点是：代码在演进，文档停在写的当天。代码有测试兜底，文档没有，所以它坏得更安静。

### P1-21 `/api/conv/51abc` 会返回 51 的会话 ✅ 已修

**现象**：

```
GET /api/conv/51abc  →  200  {"id":"51","title":"https://github.com/HerTa…",…}
```

带后缀的 id 被当成合法 id，原样返回了别人的会话。

**根因**：

```go
var n int
if _, err := fmt.Sscanf(id, "%d", &n); err != nil { ... }
```

`fmt.Sscanf` 的 `%d` **会忽略尾部垃圾字符**：`Sscanf("51abc", "%d", &n)` 得到 `n=51, err=nil`。于是任何以正确 id 开头的字符串都被接受。

**影响面**：`Get` 是读路径，泄露的是用户自己的数据（本地单用户），所以不算安全漏洞；但它让"不存在的 id"和"存在的 id"边界变得模糊 —— 比如前端拼 URL 时出了错，看到的是正常数据而不是 404，问题会被藏起来。

**顺带确认了写路径是安全的**：`DELETE /api/conv/1026abc` 返回 **404**。因为 `Archive`/`Delete` 是拿字符串直接进 SQL 的 `WHERE id=?`，SQLite 的类型亲和性把 `'1026abc'` 判为不匹配 —— 挡得住的不是代码，是数据库。只有 `Get` 先经过 Go 的解析，才漏了。

**修复**：改用 `strconv.Atoi`（严格解析），并拒绝 `<= 0`。前后空白保留容忍（URL 里偶尔会带），用 `TrimSpace` 处理。

**验证**：
- `/api/conv/51abc` → **404 没有这个会话**；`/api/conv/51` → 200 照常
- 新增 `TestGetRejectsMalformedID`：正常 id 能读；`51abc` / `''` / `0` / `-1` / `abc` / `1e3` / `0x10` 全部拒绝；`"  51  "` 前后空白仍接受

写这个测试时我自己先断言错了 —— 把 `"1 "`（尾部空格）列进了坏样本，测试立刻报红，才想起 `TrimSpace` 是有意为之。测试比我更清楚这条代码想干什么。

### P1-22 两个测试脚本会"假绿"：没数据时静默跳过 ✅ 已修

**现象**：`run2.py` 和 `run3.py` 都依赖库里存在一条任务。tasks 表为空时：

- `run2.py`：打印一句「(造任务失败，后续用例会跳过)」，然后**继续跑**，后面两个用例因为没有任务而"通过"
- `run3.py`：打印「(没有任务，跳过深链用例)」，断言写成 `(not tid) or (...)` —— `tid` 为 `None` 时**直接算通过**

**为什么这比失败更危险**：套件显示全绿，但深链那条路径**一次都没验过**。而且这个状态很常见 —— 每次我清理完测试数据，tasks 表就是空的。

**修复**：
- 两个脚本都改成自己造任务（`dbtool.new_conv()` + `POST /api/tasks` + 轮询等步骤），不再依赖前一跑留下的东西
- 造不出来就是**真失败**（`FAIL.append`），不再只是打印一句
- `run3` 的断言去掉 `(not tid) or` 那半边
- 两个脚本结尾都清理 `task_steps` / `tasks` / `outbox` 和自己的会话

**验证**：tasks 表从空开始跑，`run2` 打印「已清理测试任务与会话 41 1031」、`run3` 打印「40 1030」，都是 0 失败，跑前跑后数据都是 `21 / 0 / 0`。

**顺带扫了一遍其它"假绿"写法**：`grep` 了 `(not x) or`、`except: pass`、`跳过`。剩下的只有 `robustness.py` 里一处 `except: pass`，在"尽力停掉自己这一轮"的清理代码里 —— 那是合理的尽力而为，不属于假绿。

**这条值得单独记**：测试的价值在于它**失败时能说明问题**。一个会静默跳过的测试，比没有这个测试更糟 —— 它给了你"这里验过了"的错觉。

### 审查 `pixel_diff` 本身：它是严格的（结论：可信）

上一轮怀疑了测试脚本，这一轮怀疑视觉裁判。`pixel_diff.py` 的对比逻辑：

```python
diff = ImageChops.difference(a, b)
bbox = diff.getbbox()
if not bbox: 一致
else: px = sum(1 for p in diff.getdata() if p != (0,0,0))
```

**阈值是 0** —— 任何一个像素不同都算差异，不会因为"差异很小"放过去。方向是安全的：它可能误报（字体抗锯齿、动画时序），不会漏报。

**裁剪值也核对过**：`wide` 裁 262px，实测 `.drawer` 宽度是 250px —— 留了 12px 余量，数据驱动的会话条目不会漏进对比区。`narrow` 裁 252px 而抽屉此时是 `display:none`（宽 0），等于多裁了 252px 正文，但改前改后一致，不影响结论。

**已知盲区**（都不是漏洞，是取舍）：
- **左栏永远不参与对比** —— 因为它是数据驱动的。等价于放弃对左栏的视觉回归保护，靠 `check_layout` / `check_contrast` 兜。
- **只有 6 个视图**：会话页（两个会话）、空状态、设置、窄屏两个。任务展开、错误态、长会话滚动都不在里面 —— 这些靠功能测试覆盖。

### 对比度审计：4 类元素低于 WCAG AA（记录，未改）

`check_contrast.py` 是报告型脚本（永远 exit 0），这轮把它输出的数字算清楚：

| 元素 | 字号 | 当前对比度 | AA 要求 |
|---|---|---|---|
| `.ef-tag`（左栏会话编号） | 9px | **2.45** | 4.5 |
| `.msg .av`（每条消息的头像「Agent」） | 10px | **2.82**（89 处） | 4.5 |
| `.set-section`（设置分组标题） | 11px | **3.83** | 4.5 |
| `.src-tag builtin`（工具来源标签） | 9px | **3.83** | 4.5 |

算了一下：在任何这几种底色上要达到 4.5:1，前景都得压到 **`#505050` 左右**（现在用的是 `--c5:#778587`）。

**为什么没动**：这几个颜色属于你确认过的"左栏浅灰配色"体系 —— 它换来的是低对比的安静感，牺牲的是这些小字的可读性。这是设计取舍，不该由我按 WCAG 一刀切。真要改，最小的改动是把 `.ef-tag` 和 `.set-section` 从 `--c5` 换到 `--c6`（`#596364`，约 4.6:1），`.av` 单独调深一点。

数字记在这里，你醒了可以决定要不要动。

### P1-23 快速切会话时，慢的那个响应会覆盖当前会话 ✅ 已修

**现象**（代码分析，构造复现）：`openConv` 里

```js
const c = await api('/api/conv/' + id + '?rounds=' + PAGE_ROUNDS)
setMsgs(normalize(c.messages || [])); setTitle(c.title || '会话')
```

`await` 之后**没有校验当前会话是否还是 `id`**。用户点 A（大、慢）→ 立刻点 B（小、快），B 先画上去，A 的响应后到，把 A 的消息铺到 B 的界面上 —— **URL 是 B、内容是 A**。

同一个 `openConv` 尾部的 `attachStream(id)` 和 `loadCtx(id)` 也缺同样的校验（后者会把旧会话的上下文覆盖新的）。

**复现**：给 `/api/conv/118` 的 fetch 加 1.5 秒延迟，点 118 后 400ms 点 51。修复前能观察到消息数变成 118 的；修复后 URL 是 `?conv=51`、消息 99 条、标尺第 23 轮，完全是 51 的。

**修复**：三处 `await` 之后各加一句 `if (convIdRef.current !== id) return`。`attachStream` 里本来就有这个模式（`if (convIdRef.current !== id) return`），只是 `openConv` / `loadCtx` 没跟上。

### reasoning 链路（实测：正常，但没能观察到逐字增长）

用带思考的 profile 发了一条推理题，`.think-sum` 最终显示「思考过程 · 8601 字 · 68.2s」；切会话时 `stream.clear()` 会清掉实时缓冲（`openConv` 第一行就是），不会串台。

**但没测到"逐字增长"**：本地模型把整段 reasoning 一次吐完，第一次采样（约 1 秒后）就已经是 8601 字。这不是缺陷，是模型速度让这个观察不成立。

**顺带发现**：`attachStream`（重连）和 `send`（正常发送）里各有一份几乎相同的 SSE 事件处理逻辑（`reasoning` / `delta` / `tool` / `error` 各自 `+=`）。这是重复实现，将来改一处忘另一处的风险是实打实的 —— 但合并两个函数的改动面和风险都不小，先记着。

**又一次自己的失误**：测 reasoning 时我直接在你正在用的 conv51 里发了一条「请一步步推导 17 乘 23」—— 消息数从 575 变成 577。已删除（库里有 `agentd.db.bak-conv51b-*` 备份），确认回到 575、最后一条仍是原来的 `seq 575`。**这是这轮里第三次拿真实会话当靶子**，而且这次连副作用都预判到了却没避开。

### 两处 SSE 处理：差异是必要的，重复是多余的 ✅ 已抽公共函数

上一轮注意到 `attachStream`（重连）和 `send`（正常发送）里各有一份 SSE 处理逻辑，这轮逐事件对比。

**处理的事件确实不同，而且差异都合理**：

| 事件 | `send` | `attachStream` | 为什么 |
|---|---|---|---|
| `meta` | ✓ 设 conv_id / 标题 / 写 URL | ✗ | 重连时会话已知 |
| `reasoning` | ✓ 带 `_r0` 算耗时 | ✓ 不带 | 重连后不知道从何时起算，干脆不显示秒数（`rsecs` 为 undefined → 不渲染） |
| `delta` / `tool_start` / `tool` / `usage` | ✓ | ✓ | 内容重建必需 |
| `error` | ✓ 追加 `✗ …` | ✗ | 收尾的 `finish()` 会拉全量，错误已落库，靠刷新兜底 |
| `interject` | ✓ 状态栏提示 | ✗ | 同上 |
| `done` | ✓ 写 secs / 状态栏 | ✗ | 重连结尾走 `finish('已同步')` |

渲染节流也一致（都是 80ms 的 `pushTimer`）。**所以这不是"改一处忘另一处"的 bug，是两份场景不同的处理。**

**但拆帧那段（`buf.indexOf('\n\n')` → 逐行取 `event:` / `data:` → `JSON.parse`）是逐字相同的 15 行**。这部分没有任何场景差异，改了一边忘另一边是迟早的事。抽成 `pumpSSE(reader, onEvent)`，两边只留各自的事件分支。

结论：`attachStream` 和 `send` 的重复**不是**问题，但它们共享的解析层**是**。改完之后 JS 体积还小了 0.2KB。

**验证**：`check_live`（覆盖 `send` 路径）、`stream_test`（覆盖 `attachStream` 的重连路径）、`check_sendstop`、`robustness`、`run`、`run3` 全绿，`pixel_diff` 6/6 一致。

### P2-15 `recall` 在中文会话里的摘要永远不含关键词 ✅ 已修

**现象**：`recall` 检索命中后会给一段「…上下文摘录…」。英文会话里摘录正常地框住命中词，**中文会话里永远只给开头 120 字**，跟关键词没有关系。

**根因**：`snippet()` 里

```go
rs := []rune(s)                                    // rune 切片
if idx := strings.Index(low, strings.ToLower(w[0])); idx >= 0 {
    if idx+window < len(rs) && idx-window > 0 {    // idx 是「字节」偏移
        return "…" + string(rs[idx-window:idx+window]) + "…"
    }
}
```

`strings.Index` 返回**字节**偏移，`rs` 是**rune** 切片。中文一个字 3 字节，于是 `idx` 大约是 rune 索引的 3 倍 —— `idx + window < len(rs)` 几乎**永远不成立**，整个函数直接掉到 `truncate(s, 120)`。

代码里原来有句注释「字符索引与 rune 索引不一致时退化为前段」，但它描述的不是"有意退化"，而是**没意识到两者不同**。

**修复**：`utf8.RuneCountInString(s[:idx])` 把字节偏移换算成 rune 索引再切。

**验证**：
- 新增 `TestSnippetCJKOffset`：60 组「前缀」+ 关键词 + 60 组「前缀」，断言摘录既不是"前 120 字"、也真的含关键词
- **确认这个测试能抓到 bug**：把 `ri` 临时改回 `idx`（保留 import 占位避免编译失败），测试报红「摘录退化成了前段」；恢复后转绿。不这样验一遍，测试可能是白写的。

**顺带审查了 `recall` 的其它保护**（都没问题）：
- 正则用 Go 的 RE2 —— **没有回溯，不存在 ReDoS**；编译失败返回「✗ 正则编译失败（Go RE2 语法）：…」而不是报错崩掉（`TestRecallBadRegex` 覆盖）
- 扫描循环每条消息前 `select { case <-cctx.Done() }`，300ms 超时真的能停
- `level == "summary"` 时工具输出先截断 400 字再参与匹配

### P2-16 截图工具的 `WAYLAND_DISPLAY` 是硬编码的 ✅ 已修

**现象**（读代码发现，未造成故障）：`runScreenshot` 拼环境变量时写死了

```go
env := append(os.Environ(), "XDG_RUNTIME_DIR=/run/user/"+…, "WAYLAND_DISPLAY=wayland-1")
```

`exec.Cmd.Env` 里重复的键以**最后一个**为准，所以这句会**覆盖**从 systemd 继承来的值。这台机器上显示恰好是 `wayland-1`（已确认 `/run/user/1000/wayland-1` 存在），所以一直没出问题；但换一台机器、或者用户切到 `wayland-0` / X11，截图就会失败，而错误信息只会显示 grim 报的 socket 路径，看不出是这里写死的。

**修复**：只在环境里没有时才补默认值。

**验证**：写了个一次性探针直接调 `Registry()["screenshot"].Run(...)`，返回 `text + image(/shots/1793664506338626842.png)`；该 URL 通过服务访问得到 **200 / 3.8MB**，截图内容正常。探针和截图文件都已删除。

**顺带确认的两点**：
- 文件名用 `UnixNano()`，同一秒内连续截也不会覆盖
- 截图失败时返回 `✗ 截图失败：…` 而不是报错（模型能看到并换方式）

### P2-17 CLI `agentd chat` 的两个静默问题 ✅ 已修

顺着"硬编码/环境耦合"扫代码时拐到 CLI 入口，发现两个独立的小问题。

**（1）超长输入会让程序静默退出**

```go
sc := bufio.NewScanner(os.Stdin)   // 默认行上限 64KB
for {
    fmt.Print("\n> ")
    if !sc.Scan() { break }        // 超长时 Scan() 返回 false
```

`bufio.Scanner` 默认 `MaxScanTokenSize = 64KB`。粘贴一段大日志（我的复现是 10 万字符）→ `Scan()` 返回 false → 直接 `break` 退出，**没有任何提示**。用户看到的是"打完字回车，程序没了"。

`sc.Err()` 其实会告诉你是 `bufio.Scanner: token too long`，但代码没读它。

**修复**：`sc.Buffer(..., 4MB)` 提高上限，并在 `Scan()` 返回 false 时检查 `sc.Err()` 打印原因。

**（2）一句话没说也打印「会话已保存」**

```go
c, err := a.Conversation("")
...
fmt.Fprintf(os.Stderr, "会话已保存: %s（agentd show %s）\n", c.ID, c.ID)
```

`Conversation("")` 只是建了个内存对象，**真正落库是在第一次 `Send`**。直接空行退出时 `c.ID` 是空串，输出就是：

```
会话已保存: （agentd show ）
```

—— 既没保存，`agentd show ` 也是个无效命令。

**修复**：`c.ID == ""` 时改说「（没有对话，未创建会话）」。

**验证**：
- 10 万字符输入：不再静默退出，正常进入 `> ` 提示（4MB 上限装得下）
- 空行退出：输出 `（没有对话，未创建会话）`，不再是假话

### P2-18 `.gitignore` 的 `agentd` 没锚定，连 `cmd/agentd/` 一起忽略 ✅ 已修

**现象**：提交 CLI 修复时 `git add cmd/agentd` 报

```
下列路径根据您的一个 .gitignore 文件而被忽略：cmd/agentd
```

**根因**：`.gitignore` 第一行是 `agentd` —— 没有前导 `/` 的 pattern 会匹配**任意层级**下叫这个名字的文件或目录。本意是忽略编译出来的二进制 `./agentd`，实际把 `cmd/agentd/` 整个目录也盖住了。

**为什么一直没出事**：`cmd/agentd/main.go` 在加这条 ignore **之前**就已经被跟踪了，而已跟踪的文件不受 ignore 规则影响，所以代码一直在仓库里。出问题的只是"往这个目录里加新文件" —— `git add <目录>` 会展开并过滤，于是报 ignored。

**修复**：改成 `/agentd`，只忽略根目录那个二进制。

**验证**：改完后 `git add cmd/agentd` 正常，`git ls-files cmd/` 仍能看到 `main.go`。

**这类 bug 的特点**：它不制造错误，只是让某个操作悄悄失效/变形。如果那次提交没恰好走 `git add <目录>` 的路径，可能一直不会发现 —— 而往 `cmd/agentd/` 加新文件时就会莫名其妙地"加不进去"。

### CLI 八个子命令逐个走查（行为全对）+ P2-19 usage 提示不全

用隔离数据目录把 CLI 全跑了一遍：

| 命令 | 期望 | 实测 |
|---|---|---|
| `convs`（空库） | 无输出不报错 | ✓ |
| `send "只回复两个字：好"` | 回复「好」并落库 | ✓ |
| `convs` | 列出 1 条（id / 时间 / 标题） | ✓ |
| `show 1` | 打印标题 + 每条消息 | ✓ |
| `archive 1` | `convs` 空、`archived` 有 1 条 | ✓ |
| `restore 1` | 回到 `convs` | ✓ |
| `rm 1` | **只归档**（库中 `archived=1`，行还在） | ✓ |
| `rm 1 --hard` | 真删（表空） | ✓ |

**P2-19**：`agentd bogus` 的提示只列了 6 个命令：

```
可用: chat | send | convs | show | config | version
```

实际有 11 个 —— 少了 `serve` / `archived` / `archive` / `restore` / `rm`。尤其 `rm` 缺得不巧：它的默认行为是**归档而非删除**，只加 `--hard` 才真删，而这个信息只在用户已经知道有 `rm` 的前提下才可能被查到。

改成完整用法表，并把 `rm` 的两种语义写清楚。

**一个观察（没改）**：每条 CLI 命令都会往 stderr 打一行 `[INFO] start model=…`，脚本里用会显得吵。但它走 stderr、不影响管道内容，而且对"配置到底加载了哪个 profile"是有用的确认，先留着。


















### P1-16 同一会话并发两个请求会打乱消息顺序 ✅ 已修

**现象**：对同一个会话同时发两个 `POST /api/chat`，落库结果：

```
3 user 乙      ← 后发的请求
4 user 甲      ← 先发的请求
```

顺序颠倒。而且两边各自预分配 seq 占位，最后留一条 `running` 僵尸。

**根因**：`handleChat` 不检查这个会话是否已有轮次在跑。两个请求都能通过，各自 `append` 到 `c.Messages` 再 `Save` —— 谁先落库谁在前面。顺带 `s.convCancel[convID] = cancel` 也被后一个覆盖，`/api/stop` 只能停到后一个。

**修复**：在注册 `convCancel[convID]` 的地方做「检查并占用」：

```go
if _, busy := s.convCancel[convID]; busy {
    s.convMu.Unlock(); cancel()
    send("error", map[string]string{"error": "这个会话正在生成中，等它结束或先点停止"})
    return
}
```

**验证**：并发两次 → 先到的正常跑完（`user` + `assistant` 都 `done`），后到的收到 `event: error` + 「这个会话正在生成中」，库里没有多余消息。

### P0-6 存储写失败时静默继续：前端拿到 `conv_id: ""` ✅ 已修

**怎么发现的**：想测「磁盘写不进去会怎样」，把数据目录设成只读起实例。第一次 `chmod 444` 完全没用 —— WAL 模式下写先进 `-wal`，而那个 fd 已经打开、权限位管不着它。改成**启动前就只读**才真正复现。

**现象**（修复前）：

```json
event: meta
data: {"conv_id":"","title":"新会话"}

event: stage
data: {"stage":"generating"}
...（日志里能看到 tool.run，模型真的在跑）
event: error
data: {"error":"attempt to write a readonly database (8)"}
```

新建会话的 `c.ID` 是 `Save` 时分配的。写失败 → ID 空 → `meta` 里 `conv_id: ""`，**但服务端照样把这一轮跑完**，最后才报错。前端在这期间会把它当成「已创建」，拿空 ID 去做后续操作。

**根因**：`handleChat` 里那句

```go
// 会话一建立就先落盘：即使这一轮被中断，会话本身与用户消息也不会丢
_ = s.st.Save(c)
```

注释写着「不会丢」，错误却被丢掉了。

同一类问题在 `loop.go` 还有三处 `_ = a.st.Save(c)`：

| 位置 | 注释原话 | 实际后果 |
|---|---|---|
| 用户消息落盘 | 「请求被中断也不会丢这句」 | 起点没存住，仍跑完整轮 |
| 中断分支 | 「中断也要留住上下文」 | 已生成内容丢失且无任何提示 |
| 工具轮 | 「先落库，再执行」 | 工具真跑了，历史里却没有 `tool_calls`，下次读回来上下文是断的 |

**修复**：
- `handleChat`：检查 `Save` 错误，失败立刻 `send("error")` 并返回（不进入 `SendWithTools`）
- 用户消息落盘：失败直接 `return fmt.Errorf("保存用户消息失败：%w", err)`
- 工具轮：失败返回错误，**不执行工具**
- 中断分支：至少 `logx.Warn`，不静默

**验证**（只读目录起实例，新建会话）：

```
event: error
data: {"error":"会话保存失败：attempt to write a readonly database (8)"}
```

只有这一个事件，日志里**没有** `chat.start` / `tool.run` —— 一秒内失败，不再白跑一轮模型。

### P1-17 断线重连会把已经收到的内容再拼一遍 ✅ 已修

**现象**：SSE 断线重连后，正在生成的回复会变成两份重复（"甲乙" → "甲乙甲乙"）。

**根因**：`attachStream` 里 `live`（累积中的消息）在重连循环**外面**初始化：

```js
let live = { role: 'assistant', content: '', reasoning: '', at: now(), seq: 0 }
for (let attempt = 0; attempt < 60 && !done; attempt++) {
    resp = await fetch('/api/conv/' + id + '/stream?from=0')   // 每次都是从 0 重放
    ...
    live.content += ...                                        // 直接往后拼
}
```

而服务端的 `streamFrom` 在轮次**还在跑**时，会从 `from` 处回放整个 `job.buf`。`from=0` 就是全量重放。于是每次重连都把前面收到的 delta 再拼一遍。

**为什么一直没被发现**：刷新页面时 `attachStream` 是全新执行的，`live` 本来就从空开始，看起来完全正常。只有**同一次页面会话内**的重连（网络抖动、切 VPN、休眠唤醒）才会踩到。`stream_test` 的「刷新重连」用例验的是「数据库里的消息完整」，不是「界面上有没有重复」。

**修复**：每轮重连开始前重置 `live` 和流式状态 —— 既然服务端每次都从头重放，客户端就必须从头接。

```js
for (let attempt = 0; ...) {
    live = { role: 'assistant', content: '', reasoning: '', at: now(), seq: 0 }
    stream.clear()
    ...
}
```

**验证**：
- 读 `streamFrom` 确认语义：轮次在跑 → `snapshot(from)` 回放缓冲（`from=0` 即全量）；轮次已结束 → 只回一个空 `event: done`
- 实测两次 `curl .../stream?from=0`（轮次结束后）都只拿到 1 个 `done` 事件，符合上面的分支
- `check_live` / `check_sendstop` / `stream_test` / `robustness` / `check_api` 全绿，`pixel_diff` 6/6











### P1-10 插话失败却回 200 ✅ 已修

**现象**：`POST /api/conv/999999/interject` 返回 `{"mode":"turn","ok":true}` + **200**，可那个会话根本不存在。后台只留下一行 `[WARN] interject->turn conv=999999: file does not exist`，客户端以为「接着做」已经生效了。

**根因**：`handleInterject` 在「当前没在跑 → 当普通消息起一轮」这条分支里，把 `runTurn` 丢进 goroutine 后**立刻回 200**。goroutine 里的失败只进日志，没有任何通道能回到 HTTP 响应。兜底那句也是 `200 + ok:false` —— 状态码照样算成功。

**为什么今天才暴露**：P0-2 修好之前，`runTurn` 遇到不存在的 id 会**静默新建**，这条路径「看起来成功，居然也真的成功」；`Conversation()` 一改成报错，同一个 200 立刻变成谎言。**两个 bug 叠在一起时，前一个把后一个挡住了**。

**修复**：
- 该分支在起 goroutine 之前先 `s.st.Get(id)` 确认会话存在，读不到直接 404
- 兜底的「会话不在运行且无法起新回合」从 200 改成 **400**

**验证**：`POST /api/conv/999999/interject` → `404 {"error":"没有这个会话"}`，会话数保持 21。前端 `interject()` 本来就写了 `if (!r.ok) { setStatus('✗ 插话失败'); return }` —— 这条分支以前永远触发不到，现在有了。

### P1-11 归档 / 恢复 / 删除不存在的会话也报成功 ✅ 已修

**现象**：`DELETE /api/conv/999999` 返回 `{"archived":"999999","ok":true}` + 200。客户端以为归档成功，库里其实什么都没变。

**根因**：`SQLiteStore` 的三个方法只看 `Exec` 返回的 error：

```go
func (s *SQLiteStore) Archive(id string) error {
	_, err := s.db.Exec(`UPDATE conversations SET archived=1 WHERE id=?`, id)
	return err
}
```

而 `UPDATE ... WHERE id=999999` 命中 0 行**不算错误**，`err` 是 nil。`Restore` 和 `Delete` 是同一个写法。

**怎么发现的**：新写 `check_api.py`，对 15 个端点逐个发不同方法、比对预期状态码。31 条用例里 11 条不符合预期 —— 其中 8 条是**我自己预期写错了**（`/api/context` 和 `/api/models` 本来就要参数，400 才对），剩下 3 条里最严重的是这条。

**修复**：三个方法都检查 `RowsAffected()`，0 行返回 `os.ErrNotExist`。`Delete` 的硬删分支检查 `conversations` 的删除行数（先删 messages 可能 0 行，那是正常的），软删分支直接委托给 `Archive`。

**验证**：
- 单元测试 `TestArchiveRestoreDeleteMissingID`：四个方法对不存在的 id 都报错，**存在的会话照常成功**（不能为了判不存在把正常路径弄坏），硬删之后读不到
- 部署实测：`DELETE /api/conv/999999` → `404 {"error":"归档失败：file does not exist"}`；自建会话归档 → 200、恢复 → 200；会话数保持 21




---

## P1 — 状态错乱 / 响应式破坏 / 同角色样式不统一

### P1-1 空状态按钮与卡片按钮是两套语言 ✅ 已修

**现象**：新建会话页 `START HERE` 卡片右侧三个按钮（`新建` / `选择` / `去输入`）与右侧设置面板里的按钮观感完全不同。

**根因**：`.empty .step button`（`theme.css:81`）是 2026-10-25 样式重写**之前**留下的规则：

```css
.empty .step button{border:1px solid var(--line);background:#fff;padding:.32rem .7rem;
  font-size:.8rem;clip-path:polygon(0 0, calc(100% - 8px) 0, 100% 8px, 100% 100%, 0 100%)}
```

它用 `clip-path` 切掉右上角。而重写后确立的按钮语言是 `.btn`（`theme.css:320`）—— 直角 + 左上/右下 **L 形角标** + `--c4` 细边 + 悬停反色填充 + `0.74rem` + 字距。重写时新增了 `.btn` 体系，但空状态面板没被纳入，于是同一产品里并存两种按钮形制。

**修复**：把按钮体系从「卡片私有」提升为「全局」—— `.card .btn` → `.btn`（15 条规则）。`.btn` 此前只在 `.card` 内使用，去前缀不改变任何现有元素的计算样式。空状态三个按钮加 `className="btn"`，删除旧规则，只保留布局约束：

```css
.empty .step .btn{justify-self:end;min-width:4.4rem}
```

**验证**：
- `pixel_diff compare`：`wide-conv51` / `wide-conv118` / `wide-settings` / `narrow-conv51` **4 张逐像素完全一致**；仅 `wide-empty`(2114px) 与 `narrow-empty`(2114px) 不同，差异包围盒 `71×171` 恰好是按钮那一列 —— 证明改动没有外溢到其它页面。
- 修复前实测宽度：`新建` 43px、`选择` 42px、`去输入` 62px（右对齐、左边缘参差）；修复后三者等宽。

### P1-2 空状态按钮宽度参差 ✅ 已修

**现象**：三个按钮右边缘齐、左边缘参差，因为宽度只由 `padding + 文字数` 决定（2 字 vs 3 字）。

**根因**：`.step` 是 `grid-template-columns:2.2rem 1fr auto`，`auto` 列宽度随每个按钮文字自适应；没有任何统一宽度的约束。

**修复**：同 P1-1 的 `.empty .step .btn{min-width:4.4rem}`（`4.4rem` ≈ 70px，覆盖最宽的 3 字按钮）。

**验证**：像素差异包围盒宽 71px，与 `min-width` 一致；三个按钮左边缘对齐。

### P1-3 `isShotPlaceholder` 不看 Role ✅ 已修

**现象**：没有立即暴露的症状 —— 这正是它危险的地方。

**根因**：函数只判断「这条消息带图」且「正文以（上一个工具的截图开头」：

```go
return len(m.Images) > 0 && strings.HasPrefix(m.Content, "（上一个工具的截图")
```

没看 `Role`，所以一条 **assistant** 消息只要带了图、正文又碰巧以那句开头，也会被判成「用户提问的截图占位」。它没出事，纯粹因为唯一调用方 `convWindow` 在外部先判了 `all[i].Role == "user"` —— 换个调用方就会踩。

**怎么发现的**：写单元测试时把这个条件列进去，测试直接 FAIL（`classify_test.go:84`）。这是本轮唯一由测试而非截图发现的 bug。

**修复**：角色判断收进函数本身。同时修的时候踩了一次坑 —— 顺手把外层的 `Role == "user"` 也删了，结果 assistant 消息全被算成一轮（`total=6` 而不是 3），全量 `go test` 当场 FAIL。两个判断是两件事：函数内判 role 防误用，外层判 role 是因为「一轮必须由一条 user 消息开头」。已在代码里写清楚，免得下一个人又觉得冗余删掉。

**验证**：`go test ./...` 全绿；黑盒 `check_tools` 的 `data-seq 严格递增 n=99`、`run2` 的轮次显示都正常。

### P1-4 渲染一抛错就整页白屏 ✅ 已修

**现象**：页面上没有任何兜底 —— 某个组件渲染抛错时，用户看到的是纯白，既没有错误信息也没有刷新入口。

**根因**：入口 `main.jsx` 直接 `render(<App />)`，没有错误边界。React 18 的默认行为是渲染期抛错就**卸载整棵树**。触发源不难遇到：脏 `kv`、老缓存、后端返回了预期外的结构。

**修复**：新增 `ErrorBoundary.jsx`，在 `main.jsx` 里包住 `<App />`。出错时显示错误消息、前 6 行堆栈、「重新加载」按钮，并 `console.error` 带上 componentStack。样式复用现成的 `.btn` 和色阶变量，不引入新的视觉语言。

**验证**：
- 临时在 `App()` 顶部插一行按 URL 抛错的代码，构建后访问 `?_crash_test=1`：`.crash` 出现、`crash-msg` 是「boundary 自检：故意抛错」、按钮是「重新加载」
- 还原注入后 `grep -c _crash_test` 为 0，重新构建
- `pixel_diff` 6/6 逐像素一致 —— 正常路径完全不受影响

**边界**：错误边界只接渲染/生命周期里的同步错误，接不到事件回调和异步错误（那些仍走 `window.onerror`）。

### P1-5 样式变量散在 7 个 `:root` 里，同名变量被覆盖 2–3 次 ✅ 已修

**现象**：改一个颜色变量常常「不生效」，或者只对一部分元素生效 —— 因为它在后面被同名声明又覆盖了一次。

**根因**：`theme.css` 里有 **7 个 `:root`**，横跨 1400 行。脚本统计后：

| 变量 | 定义次数 |
|---|---|
| `--ink` / `--ink2` / `--ink3` | 各 3 次 |
| `--panel` / `--line` / `--sage` / `--sage-d` / `--side` / `--side2` / `--side-fg` / `--side-dim` / `--signal` / `--on-signal` / `--theme` / `--ef-on-signal` | 各 2 次 |

更绕的是开头那个 `:root` **引用了 `--c1..--c7`，而它们到第 397 行才定义**。CSS 变量惰性求值，所以能跑，但读代码的人会以为色阶是在开头定的。

**修复**：用脚本合并成一个 `:root`，每个变量取「最后出现者」的值 —— 同选择器、同层级（没有 `@media` 包裹），后出现者胜出，所以合并后级联结果完全一致。

做法上先保底：合并前导出 51 个变量的最终值，合并后重新解析并**逐项比对**，数量和值全部相同才写回。脚本第一次跑就报了 5 个变量丢失（`--c1`、`--paper`、`--side`、`--ef-ink`、`--warm`）—— 全是各分组的第一个，因为「分组注释 + 第一个变量」被当成同一段跳过了。修好解析器才通过。

**验证**：`pixel_diff` **6/6 逐像素一致**，文件 1420 → 1394 行。

### P1-6 `web/node_modules` 被提交进仓库 ✅ 已修

**现象**：`git ls-files` 共 2488 个文件，其中 **2352 个**是 `web/node_modules` 下的依赖 —— 占全仓 94%。`.git` 已经 43M。

**根因**：`.gitignore` 里确实有 `node_modules/`，但这些文件在加规则**之前**就已经进了索引。`.gitignore` 只对未跟踪文件生效，已经在索引里的照旧跟踪。

**后果**：每装一次依赖就产生大段无意义 diff；`git status` 长期噪音；clone 变慢；真正要 review 的改动被淹没。这不是界面 bug，但直接影响「改动能不能被看清」，属于同类问题的上游。

**修复**：`git rm -r --cached web/node_modules` —— 只移出索引，工作区文件不动 —— 然后提交。

**验证**：跟踪文件 2488 → 136；`ls web/node_modules` 内容仍在；`npm run build` 仍正常产出。


---

## P2 — 与当前风格不合时宜 / 点击目标过小

### P2-1 表单下拉框仍是浏览器默认箭头 ✅ 已修

**现象**：设置 → 模型详情的「思考强度」「服务商」两个下拉框，右侧是浏览器给出的细箭头；而输入框上方「PROFILE」下拉框用的是自定义 `▾`（深绿灰、更粗）。同一产品里并存两种下拉箭头。

**根因（已核实，非目测）**：`.composer .pick select`（`theme.css:119`）设了 `appearance:none` 并额外渲染 `<span className="caret">▾</span>`；`.card .field select`（`theme.css:303`）只给了边框和底色，**没有重置 `appearance`**，箭头因此来自 UA 而不是样式表。

证据（改前）：打开模型详情后取计算样式 —— `appearance: "auto"`、`backgroundImage: "none"`、`backgroundColor: rgb(255,255,255)`。截图 `model_before.png`（1600×1000）。

**修复**：给 `.card .field select` 重置 `appearance`，箭头用两个 45°/135° 渐变拼出 V 形 —— 之所以不用 data-URI 图标，是因为 URI 里的颜色写死，而渐变可以直接吃 `--ink2` 变量，换主题时箭头跟着变。另外把同族规则的 `background:#fff` 改成 `background-color:#fff`：`background` 简写会把 `background-image` 一起重置，不改这里箭头就会被吃掉。

**验证**：
- 计算样式：`appearance:"none"`，`backgroundImage` 为两个 linear-gradient
- **顺带修掉一处高度不齐**：改前 select 高 30px、input 高 35px（`getBoundingClientRect`）；改后 select 35px，与全部 7 个 input 齐平
- 像素对比：`wide-conv51` 内容区（x>260）**完全一致**，证明不影响会话页；`wide-settings` 的差异只有 `x 1521–1539` 一条 18px 竖带，即控件右端箭头/spinner 所在

### P2-2 数字输入框的原生 spinner ✅ 已修

**现象**：模型详情的「上下文窗口 / 输入上限 / 压缩阈值」、设置一级卡的「轮次上限 / 未活跃天数 / 保留天数」，右端都有 UA 的上下小箭头（spinner）。

**根因**：`<input type="number">` 未处理 `appearance` 与 `::-webkit-inner-spin-button`。已核实这些字段确实是 `type="number"`（`ModelForm.jsx:43/48/53`、`ListCard.jsx:30/45/70`、`ToolCard.jsx:34`）。

**修复**：`.card .field input[type=number]{appearance:textfield}`，并隐藏 `::-webkit-outer-spin-button` / `::-webkit-inner-spin-button`。

**判断依据**：这些字段里只有「轮次上限 50 / 未活跃天数 30 / 保留天数 7」是 ±1 有意义的小整数；模型那边的都是五位数（131072、105000），spinner 每次 ±1 毫无意义，而它本身只有 8px 宽，属于点击目标过小。

**验证**：6 个 number input 的 spinner 全部消失，截图 `model_after.png` 确认；输入框高度与其它 input 一致（35px）。

### P2-3 设置开关的可点高度只有 20px ✅ 已修

**现象**：设置页三张卡片（工具调用轮次上限 / 自动归档 / 截图清理）右上角的开关，实测 38×20。

**根因**：`.tg{width:38px;height:20px}` 写死，旋钮 16×16。WCAG 2.5.8 的最小目标尺寸是 24×24 CSS px，高度差 4px。

**修复**：`height:20px → 24px`，旋钮 `16×16 → 20×20`，位移 `translateX(18px) → 15px`（38 宽去掉 1px 边框是 36；旋钮 20、左起 1，到右端需移 15）。标题栏固定 36px 高，24px 的开关放得下，不撑高。

**验证**：
- 新增 `scripts/ui-test/check_targets.py`，扫所有可点元素，阈值 24×24
- 修复前：设置页 6 处、模型详情 15 处；修复后：两边都只剩 3 处正文链接
- 排除了两类假阳性：标尺刻度 / 拖拽把手（刻意做小）、`<input type=checkbox>`（被 `<label>` 包着，实际可点区 177×33）

**判定为不是问题的**：正文里的 3 个链接实测 `351×21`、`360×21`、`450×21`。高度差 3px，但面积 7000+ px²，且 WCAG 2.5.8 对「处在句子中的行内链接」有明确豁免。

### P2-4 次要文字对比度最低到 2.24:1 ✅ 已修

**现象**：状态栏、系统提示、模型名、快捷键提示这些字很淡；9–12px 的小字在浅底上看着「若有若无」。

**根因（量出来的，不是感觉）**：新增 `check_contrast.py`，按 WCAG 相对亮度公式逐个文本节点算对比度（AA 要求正文 4.5:1）。结果：

- `--c8:#A3B0AD` 被 7 处共用（`.status`、`.sysnote`、`.composer .model`、`.composer .hint`、`.composer .row1`、`.side .title .no`），实测 **2.24:1**
- `--ink3:#6E7C7D` 用在 `.eb`、`.field-label`、`.set-meta`、`START HERE` 等处，白底 **4.34:1**

这两个值都是 J 视觉层（`theme.css:1192–1193`）改出来的，比原来的 `--c5` 还淡 —— 「次要文字」被一路淡到看不清，而 `.status` 恰恰是用户看「已停止 / 出错」的地方。

**修复**：`--c8` #A3B0AD → **#637172**（2.24 → 4.64），`--ink3` #6E7C7D → **#637072**（4.34 → 5.13）。取值是反解公式得来的，不是试出来的：白底要 4.5:1，相对亮度得 ≤ 0.1833；纸色 `#F3F5F5` 背景更严，所以留了余量。

**验证**：
- 复测：会话页不达标组 4 → 2，空状态 6 → 1，设置 9 → 4，模型详情 10 → 4
- 剩下的全是 9–11px 装饰性角标（`#1` 归档标记、`◈` 头像、`内置`、`常规配置`），来自 `--c5`，属点缀而非必读信息；要改得连带动边框和图标，面太大，未动，记录在此
- `pixel_diff`：差异像素仅 0.13%–0.63% 且散布整个文字区 —— 是字色变深后的抗锯齿差异，**布局零位移**

### P2-5 没有 favicon，每次请求都 404 ✅ 已修

**现象**：浏览器标签页是通用文档图标；`curl /favicon.ico` 返回 404，控制台里也一直挂一条。

**根因**：`index.html` 只有 `<title>`，没有 `<link rel="icon">`，`web/public/` 下也没有图标文件。

**修复**：新增 `web/public/favicon.svg` —— 与界面同一套语言：纸色底 `#F3F5F5`、直角细边 `#3A4142`、中间一块色阶里的 `#99AAAD`。`index.html` 加 `<link rel="icon" href="/favicon.svg" type="image/svg+xml">`。

**验证**：构建后 `dist/favicon.svg` 存在，`curl http://127.0.0.1:18400/favicon.svg` 返回 **200**。

### P2-6 光标反馈不统一 ✅ 已修

**现象**：同样是下拉框，输入框上方「PROFILE」是默认光标，设置里「思考强度 / 服务商」是手型；点 `PROFILE` 那一整块（label）也没有手型提示。

**根因**：`cursor:pointer` 是**逐条规则**补的 —— 只在 `.card .field select` 上写过，`.composer .pick select` 和包着它的 `.composer .pick` 都没有。而 `<select>` 的 UA 默认 cursor 是 `default`，不会自动变手型。

**修复**：提到全局 `select{cursor:pointer}`，删掉 `.card .field select` 里的重复声明；`.composer .pick`（label 包着 select，点它同样会展开）单独补 pointer。

**怎么发现的**：新增 `check_cursor.py`，扫所有可点元素的 hover 反馈。这个脚本同时确认了两处**本来正确**的设计，省得以后被误改：
- `.send:disabled{cursor:not-allowed}` —— 禁用态就该是禁止光标，实测填入文字后立刻变成 `pointer`
- 9 个 checkbox 的 `default` —— 真实点击区在包着它们的 `<label>`（实测 177×33），checkbox 本身给 default 是对的

**验证**：不达标组 会话 2→1 / 空状态 2→1 / 设置 3→2 / 模型详情 4→3。剩下的是 `label.field`（点它聚焦输入框）和 label 里的 checkbox，属于合理行为，不算问题。`pixel_diff` 6/6 一致（光标不参与渲染）。

### P1-7 设置面板打开后，键盘焦点困在背景里 ✅ 已修

**现象**：用 `?settings=1&conv=51` 打开设置，连按 **30 次 Tab 都没进得去**。焦点一路在背景会话页的 `.think-sum`（每轮一个「思考过程」折叠按钮）上打转 —— conv51 有 99 条消息，按几十次也走不完。

**根因**：两个问题叠加。
1. 设置面板渲染在 `<main>` 内、composer 之后，DOM 顺序排在会话内容后面，键盘必须先把整个背景走一遍才碰得到。
2. 背景没有 `inert` / `aria-hidden`：被 `.set-scrim` 遮住了，却仍然可聚焦、可 Tab、可被读屏读到。

视觉上有遮罩，键盘用户面对的是「看不见的页面在抢焦点」。

**修复**：设置面板打开时给背景容器加 `inert` —— `.topbar`、`.drawer`、`.side-toggle`、`.thread`、`.composer`、`.rail` 共 6 处。React 18 不认 `inert={true}`（会当非布尔属性报警），所以传空字符串：`inert={showSet ? '' : undefined}`。

**怎么发现的**：新增 `check_focus.py`，用 WebDriver **真的按 Tab 键**而不是程序化 `focus()` —— 后者不触发 `:focus-visible`，测不出真实键盘路径。顺带确认了焦点可见性本身没问题：每步都有 outline 或 box-shadow，没有一处「不可见」。

**验证**：
- 打开时 `document.querySelector('.thread').inert === true`，关闭后为 `false`
- 打开后 Tab：第 1 步就落在面板内容 `DIV.card-bd`，接着 `tg on` → `input` → `textarea.ui-quips` → `btn 立即清理`
- 关闭后 Tab 恢复：`DIV.list` → `BUTTON.new-conv` → `BUTTON.side-toggle`
- `pixel_diff` 6/6 一致（`inert` 不影响渲染）

### P1-8 打开/关闭设置时焦点不跟随 ✅ 已修

**现象**：点设置按钮打开面板后，`document.activeElement` 仍是 `BODY`；按 Esc 关掉后也停在 `BODY`。键盘用户得从页面最开头重新 Tab，读屏用户不知道面板已经打开、也不知道自己回到哪了。

**根因**：`useSettings` 只管 `showSet` 这个布尔值，完全没有焦点管理 —— 打开不送进去，关闭不还回去。

**修复**：在 `useSettings` 里加一个 effect：
- 打开：把 `document.activeElement` 记成归还目标，然后送焦点进面板第一个**可见**的可聚焦元素
- 关闭：把焦点还给那个目标

踩了两个坑，都记在代码注释里：
1. **`setTimeout(0)` 不够**。点按钮打开时，浏览器要等这一轮点击的默认聚焦处理完，单个宏任务会被它盖掉 —— 实测焦点仍停在触发按钮。换成双 `requestAnimationFrame`。
2. **第一个匹配元素是不可见的**。`querySelector` 拿到的是 `.card-back.narrow-only`（窄屏专用的返回按钮），宽屏下 `display:none`，对不可见元素调 `focus()` 静默失败。改成筛 `getClientRects().length > 0`。

**验证**：打开后焦点在 `BUTTON.tg`（面板里的开关），Esc 后在 `BUTTON.dots`（触发按钮）。`pixel_diff` 6/6 一致 —— Firefox 的 `:focus-visible` 只在键盘交互时画环，程序化 `focus()` 不画，所以截图不受影响。

### P2-7 设置面板缺 dialog 语义 ✅ 已修

**现象**：P1-7 补了 `inert` 背景、P1-8 补了焦点进出，但 `.set-cols` 在无障碍树里只是一段普通内容 —— 读屏软件不知道这是个「能关掉的对话框」，也不会播报它的名字。

**修复**：`role="dialog"` + `aria-modal="true"` + `aria-label="设置"`。这三项和已有的 `inert` 是一套：`inert` 告诉辅助技术「后面不可交互」，`dialog` 告诉它「这是个对话框、叫什么名字」。缺一半等于只说了一半。

**验证**：`role` / `aria-modal` / `aria-label` 实测为 `dialog` / `true` / `设置`；`pixel_diff` 6/6 一致。

### P2-8 禁用控件没有视觉区分 ✅ 已修

**现象**：插件编辑里的「插件名」字段是 `disabled` 的，但看起来和可编辑字段一模一样。

**根因**：禁用态样式只写在 `.send:disabled` 一处，`input` / `select` / `textarea` / `button` 全都没有。在这套界面里，「禁用」约等于「看不出能不能改」。

**修复**：加统一规则 `input:disabled,select:disabled,textarea:disabled,button:disabled{opacity:.45;cursor:not-allowed}`。`.send:disabled` 的特异性更高（0,2,0 vs 0,1,1），原有的 `.38` 保持不变。

**验证**：临时建四种控件实测 `opacity` 都是 `0.45`、`cursor` 都是 `not-allowed`；`pixel_diff` 6/6 一致（现有页面里只有 `.send` 处于禁用态，而它没被覆盖）。

### P2-9 五个端点对任何 HTTP 方法都应答 ✅ 已修

**现象**：`POST /api/health`、`PUT /api/conv/51`、`DELETE /api/tools`、`DELETE /api/tasks`、`POST /api/shots/stats` 全部返回 **200 + 正常数据** —— 方法完全不影响行为。`PUT /api/conv/51` 尤其明显：它落到最后那个「读会话」分支，把 PUT 当 GET，返回了一整段会话 JSON。

**根因**：`mux` 只按路径分发，这几个 handler 里没有方法判断。

**影响**：不造成数据损坏（这些分支本身只读），但契约是松的 —— 客户端写错方法也会「成功」，问题被推迟到别处才暴露。

**修复**：五个 handler 各加一行判断，返回 405 并给出明确原因（`"GET only"` / `"GET/POST only"`）。

**验证**：`check_api.py` 31 条用例从 11 条不符合预期降到 **0**。

**顺带纠正了我自己 8 条错误预期** —— 这部分值得记下来：
- `/api/context` 和 `/api/models` 缺参数时返回 **400** 而不是 405：参数校验在方法判断之前，400 更准确
- `/api/plugins` 的 GET 是 **405** 而不是 200：插件列表走 `/api/tools`，这个端点只收 POST/PUT/DELETE

写契约测试时很容易把「我以为的契约」当成「应有的契约」。31 条里 8 条是我错，占了四分之一。







---

## 未修的风险（需要你决定）

### R-1 监听 `0.0.0.0` 且无认证

`AGENTD_ADDR=0.0.0.0:18400`，同网段任何设备都能：

- 读全部对话（`GET /api/convs`、`GET /api/conv/<id>`）
- **让模型执行任意命令**（`POST /api/chat`，模型手上有 `bash`）
- 改配置（profiles 里存着 API Key）

这大概率是你为了手机或其它机器访问特意设的，所以**没动**。三种收窄方式，按侵入性排：

1. `AGENTD_ADDR=127.0.0.1:18400` —— 只有本机能连，会失去局域网访问
2. 前面挂一层反向代理做 Basic Auth
3. 再加一个 `Origin` / `Sec-Fetch-Site` 校验（`requireJSONBody` 已经挡了跨站写请求，但读请求还是开放的）

### R-2 Host 头不校验（DNS rebinding）

`curl -H "Host: evil.com"` 照样 200。攻击者可以让 `evil.com` 先解析到自己、再 rebind 到 `127.0.0.1`，浏览器就认为同源了，能绕过 CORS 直接调 API。

**为什么没直接修**：Host 白名单容易误伤（你可能用 `agentd.local` 之类 mDNS 名访问）。要修的话我建议「只允许 localhost + 纯 IP 字面量，拒绝其它域名」，但需要先确认你平时怎么访问。

---

## 原待核实项（已全部排除）

审计过程中记下的 4 条疑点，都拿到 DOM 证据后排除 —— 没有悬空项。推理过程见下一节。

| 编号 | 观察 | 结论 |
|---|---|---|
| Q-1 | 900px 宽下顶栏左端被抽屉压住 | **已排除**。`.topbar` 在 `.work` 之前、全宽独立一行，`.mobrail` 的内容（`23/23`、`74%`、`+`、`⋯`）本来就横跨整行；抽屉在它下方左起。截图上的「被压住」是误读。 |
| Q-2 | 空状态页右栏显示 `01 / 1`，会话页显示 `23 / 23` | **已排除**。两页的 `.rn` 都在 y=150，位置一致 |
| Q-3 | 右栏底部同时出现 `−`、`-`、`+` 三种符号 | **已排除**。上下文进度条的框线 + 2px 填充 + 新会话按钮 |
| Q-4 | 抽屉底部在 `WORKSPACE / AGENT` 下方留约 35px 空白条 | **已排除**。`.drawer-foot` 一直延伸到 914（视口底） |

---

## 审计中排除的误报

产出不只是「修了什么」，「确认了什么本来就没问题」同样要留证据 —— 否则下一个人会把同样的疑点再查一遍，甚至把正确的设计改坏。

下面 7 处都是先看截图/代码觉得有问题、拿到 DOM / 计算样式 / 实测后判定为正确的：

| 疑点 | 目测结论 | 实测结论 |
|---|---|---|
| `PROFILE` 下拉框是浏览器默认样式 | 灰底 + 原生箭头，在满屏直角里很突兀 | `.composer .pick select{appearance:none}` 且额外渲染了 `<span class="caret">▾</span>`；灰底是刻意的 `--c2`。**误判** |
| 900px 下顶栏被抽屉压住 | 截图左上角像是被截断 | `.topbar` 在 `.work` 之前、全宽独立一行，`.mobrail` 本来就横跨整行，抽屉在其下方。**误判** |
| 右栏 `23 / 23` 被挤成 3 行 | 高 59px，像折行 | `writing-mode:vertical-rl` 下宽高语义相反：59px 是竖排后的行内长度，本来就是一行。加 `nowrap` / `height:auto` / 改 `flex` 后实测全部不变。**误判** |
| 右栏同时出现 `−`、`-`、`+` 三种符号 | 符号不统一 | 分别是上下文进度条的框线、2px 的填充、新会话按钮。**误判** |
| 抽屉底部留 35px 空白条 | 没撑满 | `.drawer-foot` 一直延伸到 914（视口底）。**误判** |
| `（已停止）` 占了消息编号 | 有 `[No.149]`，像普通消息 | 那就是 `SysNote` 组件，`[No.NNN]` 是定位锚点，不占轮次编号。**误判** |
| `vendor/endfield/endfield.css`（2232 行）是 J 视觉层之后的死代码 | theme.css 里已有 `.ef-*` 覆盖，像是可以删 | 实测临时去掉它的 `<link>` 再构建：`wide-conv118` 有 **278213 个像素**不同、消息区大片变化。endfield 仍提供 `.ef-prose` / `.ef-field` / `.ef-panel` 等基础形制，theme.css 只是覆盖了一部分。**不是死代码，不能删** |

教训：**截图只能提出问题，不能判定问题**。上面每一条如果直接动手「修」，都会把本来正确的设计改坏 —— 而且改完还会因为测试全绿而自以为成功了。

### 文本边界（实测：没问题）

新增 `check_text.py`：直接往库里塞 6 种极端内容 —— emoji、RTL 阿拉伯文、400 字符无空格串、控制字符（`\x00\x01\x07`）、超长 URL、中英混排。

结果全过：6 条消息都渲染出来、`scrollWidth == innerWidth`（无横向溢出）、没有消息卡越界、无 JS 错误、正文区域有内容。

### Esc 级联（实测：四层都对）

```
进编辑器     set ✓  detail ✓  editor ✓
Esc 1       set ✓  detail ✓  editor ✗   ← 退回详情
Esc 2       set ✓  detail ✗            ← 退回列表
Esc 3       set ✗                      ← 关闭整个设置
```

在这之前（上上轮之前）**根本没有 Escape 处理**，只能点 × 或遮罩关闭。

### 流式结束后会不会冲掉分页窗口？（实测：不会）

`attachStream` 的 `finish()` 每轮结束会拉一次 `api('/api/conv/' + id)` —— **全量**。575 轮的 conv51 这个响应是 **1.79MB**，直觉上它会把「只显示 10 轮」的窗口替换成全部消息，DOM 直接爆炸。

实测（自建 15 轮会话）：
- 打开后 `.msg` = 20 条
- 发一句「只回复两个字：好的」，等状态变成「完成 · 4.2s」
- 结束后 `.msg` 仍是 **20 条**，窗口从「第 6 轮」滑到「第 7 轮」，最后一条是 `No.032 … 好的`

也就是说窗口是**滑动**的，新回复正常出现，没有 DOM 爆炸。

全量拉取仍然是一次可以省掉的传输（1.79MB → 584KB），但本地耗时 10ms，用户感知不到；改 `finish()` 的分页参数又得动流式状态机。收益和风险不成比例，**保持现状**，记在这里免得下次又被人当 bug 改。

---

## 单元测试

`go test ./...` 全绿（15 个测试文件）。本轮新增 `internal/web/classify_test.go`，覆盖：

- `classifyMessage` 的 10 个分支：普通正文 / 历史数据（status 空）/ streaming / 有正文的中断 / 只有思考的中断 / 空壳中断 / 上游报错说明 / timer / handoff / 旧数据前缀
- `legacySysNote` 正反向：能认「（已停止）」「——（已达到工具轮次上限）」，不能认「前半（已停止）」（括号在句中）
- `isShotPlaceholder`：没图不算、有图无前缀不算、assistant 不算
- `convWindow` 的 `from/to/total/hasMore`：全量、只要最后 N 轮、`before` 翻页、`before` 越界、`around` 跳转、空会话
- `convWindow` 排除截图占位消息（否则标尺多一格）

**测试直接抓出一个真 bug**：`isShotPlaceholder` 只判断「有图 + 固定前缀」，不看 `Role`，assistant 消息只要带图也会被判成占位消息。当时没出事，纯粹因为 `convWindow` 外层先判断过一次 `role == "user"`。已把 role 判断挪进函数本身。

修的时候还翻了一次车：顺手把外层的 `Role == "user"` 也删了，结果 assistant 消息全被算成一轮（`total=6` 而不是 3），全量 `go test` 当场 FAIL。两个判断是两件事 —— 函数内判 role 是防别的调用方误用，外层判 role 是因为「这一轮必须由一条 user 消息开头」。已加注释写明，避免下次又有人觉得冗余。

### 新增的黑盒测试脚本

| 脚本 | 验什么 |
|---|---|
| `check_targets.py` | 全部可点元素的尺寸，阈值 24×24（WCAG 2.5.8） |
| `check_contrast.py` | 逐个文本节点的前景/背景对比度，按 WCAG 相对亮度公式算，AA 阈值 4.5（大字 3.0） |
| `check_xss.py` | 绕过 API 直接往库里塞 8 种 XSS 载荷（含 user/assistant 两种角色），验证 DOMPurify 链路：无执行、无 script、无内联事件、无 `javascript:` 链接、无 iframe 注入 |
| `dbtool.py` | 测试会话的新建与真删，四个脚本共用 |
| `check_text.py` | 往库里塞 emoji / RTL / 无空格长串 / 控制字符 / 超长 URL，验证渲染不溢出、不报错 |
| `check_cursor.py` | 可点元素的 `cursor`，专抓 `<button>` 默认不是 pointer 这类漏写 |
| `check_focus.py` | 用 WebDriver **真按 Tab** 走一遍，看焦点落在哪、有没有可见的 outline / box-shadow |

### 回归结果（2026-11-03，第 21 批之后）

**断言型**（失败会 exit ≠ 0）：

| 套件 | 结果 |
|---|---|
| `go test ./...` | 7 个包全 `ok`（19 个测试文件） |
| `check_layout` / `check_tools` / `check_settings` / `check_live` / `check_sendstop` | 全绿 |
| `run` / `run2` / `run3` | 全绿 |
| `stream_test` | 11/11 |
| `robustness` | 0 个问题 |
| `check_xss` | 8 种载荷 × 2 种角色，0 执行、0 注入 |
| `check_text` | 6 种极端文本，无横向溢出、无 JS 错误 |
| `check_focus` | 焦点不可见 0 处（修前 1 处） |
| `check_api` | 41 条（端点 × 方法 + 3 条目录遍历 + 7 条 CSRF 的 415），全部符合预期 |

**报告型**（输出清单供人工判断，不设 exit 门槛）：

| 脚本 | 当前状态 |
|---|---|
| `check_targets` | 只剩 3 处正文行内链接（`351×21` 等，WCAG 2.5.8 对行内链接有豁免） |
| `check_contrast` | 不达标组 会话 2 / 空状态 1 / 设置 4 / 模型详情 4，全是 `--c5` 上的装饰角标 |
| `check_cursor` | 只剩 `label.field` 与 label 内的 checkbox，均属合理行为 |

**视觉**：`pixel_diff` 6/6 逐像素一致。
**数据**：会话数 21，库里无 `ui-test` / `ui-rb` / `stream-test` / `ui-xss` / `ui-text` 遗留。

---

## 当前界面

2026-11-03 实拍。重拍命令：`python3 scripts/ui-test/shots.py`（改动视觉后跑一次即可）。

**会话页** 1600×1000 —— 分页 10 轮、右栏竖排标尺、标题只显示用户当轮原话：

![会话页](screenshots/01-conversation.png)

**新建会话** —— `START HERE` 三个按钮已并入 `.btn` 体系并等宽：

![空状态](screenshots/02-empty.png)

**设置** —— 开关 38×24，数字输入不再带原生 spinner：

![设置](screenshots/03-settings.png)

**模型详情** —— 下拉框是自定义 V 形箭头，与 `PROFILE` 同一形制：

![模型详情](screenshots/04-model-detail.png)

**窄屏 900** —— 抽屉占 250，内容区自适应：

![窄屏](screenshots/05-narrow.png)

**移动端 500**（Firefox headless 的最小窗口宽度）—— 抽屉收进顶栏，无横向溢出：

![移动端](screenshots/06-mobile.png)

---

## 批次记录

| 批次 | 内容 | commit | 视觉验证 |
|---|---|---|---|
| 0 | 脚手架：geckodriver 卡死自愈 + 脚本路径统一到 `__file__` | `214c7f8` | 无视觉改动 |
| 1 | P1-1 + P1-2 空状态按钮并入 `.btn` 体系并等宽 | `11dd27e` | 4/6 逐像素一致；差异仅两张空状态图，包围盒 `71×171` 恰为按钮列 |
| 2 | P0-1 测试脚本不再往真实会话写数据 | `f6d1ab1` | 无视觉改动 |
| 3 | P2-1 + P2-2 下拉箭头统一 + 去 spinner | `9ab1b8b` | 会话页内容区逐像素一致；设置页差异仅 `x1521–1539` 竖带 |
| 4 | P2-3 开关加高 + `check_targets.py` + `pixel_diff` 裁剪数据区 | `7f312dc` | 重拍基线后立即 compare：6/6 一致 |
| 5 | P1-3 `isShotPlaceholder` 补 role + 单测 + `dbtool.py` 统一清理 | `0948a09` | 无视觉改动 |
| 6 | P2-4 `--c8` / `--ink3` 调到 AA 达标 + `check_contrast.py` + `check_xss.py` | `743eda0` | 刻意改字色：差异像素 0.13%–0.63%，布局零位移 |
| 7 | P1-4 `ErrorBoundary` 兜住渲染错误 | `99b8cb1` | 正常路径 6/6 逐像素一致（包一层不影响渲染） |
| 8 | P1-5 7 个 `:root` 合并为 1（51 个变量逐项比对） | `c2377cc` | 6/6 逐像素一致，1420 → 1394 行 |
| 9 | P2-5 favicon + `endfield.css` 死代码核实 | `35dee65` | 无视觉改动 |
| 10 | P1-6 停止跟踪 `web/node_modules`（2488 → 136 个文件） | `36121c2` | 无视觉改动 |
| 11 | P2-6 下拉框光标统一 + `check_cursor.py` | `59564f6` | 6/6 一致 |
| 12 | P1-7 设置面板打开时背景 `inert` + `check_focus.py` | `bc23147` | 6/6 一致 |
| 13 | P1-8 设置面板焦点进出闭环 | `3e5b25a` | 6/6 一致 |
| 14 | P2-7 设置面板 dialog 语义 | `54de787` | 6/6 一致 |
| 15 | P2-8 禁用态统一 + `check_text.py` | `b1f3872` | 6/6 一致 |
| 16 | P1-9 多行输入框焦点可见（删 `outline:none`，不加特异性） | `10aa755` | 6/6 一致 |
| 17 | P0-2 `Conversation()` 不再静默新建 + 单测 | `63e4158` | 无视觉改动 |
| 18 | P1-10 插话失败不再回 200（先验会话 + 400） | `cd04cf8` | 无视觉改动 |
| 19 | P1-11 归档/恢复/删除检查影响行数 + `check_api.py` | `6f2bffe` | 无视觉改动 |
| 20 | P2-9 五个端点补方法校验 + 修正 8 条错预期 | `868a5ba` | 无视觉改动 |
| 21 | P0-3 `/shots/` 目录遍历（可下载整库）修复 | `81b1c27` | 无视觉改动 |
| 22 | P0-4 CSRF（`requireJSONBody`）+ P2-10 安全响应头 | `95aa742` | 无视觉改动 |
| 23 | P1-12 `/api/wake` 校验会话存在 + 清孤儿 outbox | `09e32ef` | 无视觉改动 |
| 24 | P1-13 `/api/tasks` 派发前校验父会话 | `9d9d86d` | 无视觉改动 |
| 25 | run2/run.py 自建数据并清理（修 pixel 误报） | `606e43c` | 无视觉改动 |
| 26 | P1-14 只在真压缩时报 compacting + 新装路径走查 | `9c34426` | 无视觉改动 |
| 27 | P1-15 `AGENTD_DATA_DIR` 统一 13 处硬编码路径 + 错误路径验证 | `e5c860f` | 无视觉改动 |
| 28 | 真压缩 / 上翻分页 / 时间格式的验证记录 | `3150b93` | 无视觉改动 |
| 29 | P0-5 断开即 panic + P1-16 同会话并发 | `aef86f6` | 无视觉改动 |
| 30 | P0-6 存储写失败不再静默继续（4 处 `Save` 错误） | `cd8cf48` | 无视觉改动 |
| 31 | P1-17 断线重连不再拼接重复内容 | `71e7aa8` | 无视觉改动 |
| 32 | P0-5 补刀：`job.go` 里 4 处裸 `Flush` | `e76efaf` | 无视觉改动 |
| 33 | 新增 `check_paging.py`（16 条边界断言）+ 极端标题实测 | `c89f94e` | 无视觉改动 |
| 34 | P1-18 标尺间距自适应（60 轮内视觉不变） | `1459ed3` | 刻意改视觉：>61 轮时 gap 压缩；6 张对比图仍一致 |
| 35 | 新增 `check_scale.py`（7 条标尺断言） | `174dd39` | 无视觉改动 |
| 36 | P1-19 子会话编号跨位数错乱（字符串排序）+ 父归档实测 | `e43eba9` | 无视觉改动 |
| 37 | handoff 并发与失败恢复实测（均正常，未改代码） | 无提交 | 无视觉改动 |
| 38 | P2-11 插话按钮说明它下一轮才生效 | `4e9fe1c` | 只改文案，pixel 6/6 一致 |
| 39 | P1-20 补 `/api/tasks/{id}/cancel` + P2-12 文档漂移 7 处 | `edb195b` | 无视觉改动 |
| 40 | P2-13 第二批文档漂移 6 处（DESIGN/UI/README） | `6c4a513` | 无视觉改动 |
| 41 | P1-21 会话 id 严格解析（`Sscanf` 忽略尾部） | `a0a32c4` | 无视觉改动 |
| 42 | P1-22 run2/run3 假绿：没数据时静默跳过 | `5ee5edf` | 无视觉改动 |
| 43 | 审查 pixel_diff（严格，可信）+ 对比度量化（未改，待你决定） | 无提交 | 无视觉改动 |
| 44 | P1-23 快速切会话的响应竞态 + reasoning 实测 | `c6f1e1a` | 无视觉改动 |
| 45 | 抽取 `pumpSSE`（两处重复的 SSE 拆帧） | `2ecd40d` | 无视觉改动 |
| 46 | P2-15 `recall` 摘要按字节索引切 rune（中文永不命中） | `b3f5220` | 无视觉改动 |
| 47 | P2-16 截图工具 `WAYLAND_DISPLAY` 硬编码 | `0ae2a2f` | 无视觉改动 |
| 48 | P2-17 CLI 超长输入静默退出 + 假「会话已保存」 | `128dbbf` | 无视觉改动 |
| 49 | P2-18 `.gitignore` 未锚定（忽略 cmd/agentd） | `128dbbf` | 无视觉改动 |
| 50 | CLI 八命令走查 + P2-19 usage 提示不全 | `5d8cc05` | 无视觉改动 |

基线位置：`~/.data/agentd/pixel-base/`（不放 `/tmp`，避免被系统清理后无法对比）。
