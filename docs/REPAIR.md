# xaihi 修复记录

上游代理 400（Bad request syntax）
- 现象：curl 正常，但应用里报 400，错误里能看到 "{...}POST /180/v1/chat/completions HTTP/1.1" 这种拼接。
- 原因：本地 llm-proxy 对 keep-alive 复用连接处理有误。
- 修法：客户端禁用复用：http.Transport{DisableKeepAlives:true, DisableCompression:true} + req.Close = true。

改了 CSS 但浏览器里看不到该属性
- 现象：写了 grid-template-columns，DevTools 里根本没有这一项。
- 原因：同一元素上 grid-template-areas 的列数与 grid-template-columns 不一致时，浏览器判定整条 grid 声明非法并丢弃。
- 排查：先看 computed 是否为空，再回头数区域列数；不要继续追加覆盖。

!important 压过内联样式
- 现象：JS 里 element.style.height = ... 不生效。
- 原因：样式表的 !important 优先级高于内联（无 !important）声明。
- 修法：去掉样式表里的 !important，或改用 CSS 变量（JS 写 --h，样式里 height:var(--h)）。

面板能看见但点不动
- 原因：给祖先元素加 z-index 会形成层叠上下文，子元素再高的 z-index 也出不来，被后绘制的遮罩盖住。
- 修法：不要给容器乱加 z-index；只在需要压层级的两者之间排定。

字体不一致导致"没垂直居中"
- 现象：同一行文字看着基线不齐。
- 原因：endfield 的 .ef-read / .ef-prose 自带 --ef-font-* 变量，回落到 Noto，与全局字体不同。
- 修法：把 --ef-font-ui / --ef-font-text / --ef-font-mono 也指向同一字体。

## 2026-10-29 `go test` 挂死在 wakeToken

现象：`/api/wake` 的单元测试永远停在 `=== RUN`，`-timeout` 触发后才看到栈：
`os.ReadFile(/dev/urandom)` 永不返回。

原因：`/dev/urandom` 是字符设备，`os.ReadFile` 会一直读到 EOF（永远没有），把进程读死。

修法：改用 `crypto/rand.Read`。教训：读"随机源"永远不要用 ReadFile，测试里也别用无超时的
外部调用（`go test -timeout` 是唯一的兜底）。

## 2026-10-30 「kb_search 要 32 秒」是假的

现象：日志里 `tool.call name=kb_search ms=32261`，看起来本地检索极慢；但直接跑 `kb search` 是 0.00s。

原因：`handleChat` 里的 `toolStart` 是**跨工具共享**的，而工具是并行的；`onTool` 每完成一个就
`time.Since(toolStart)` 再重置 → 记到的是"距上一个工具完成的时间"，里面混进了下一轮的模型生成时间。

修法：耗时在 agent 的工具 goroutine 里就地测量（`tool.run ms=…`），随 `onTool` 传给 web，
写进 SSE（`tool.ms`）与 `messages.tool_ms`。实测 kb_search 11~16ms、kb_get 9~11ms。
教训：**并行任务的共享计时器一定会撒谎**，计时必须在任务自己内部做。

## 2026-11-10 改名迁移时 SQLite 的 WAL 没跟着搬，丢了 30 条消息

改名是 `mv agentd.db xaihi.db`。问题：SQLite 开了 WAL 模式，主库旁边还有
`agentd.db-wal` / `agentd.db-shm` 两个附属文件，它们**按文件名绑定主库** ——
只改主库名，WAL 就成了孤儿，里面 1009 帧（4.1MB）未合并的数据直接被忽略。

发现：迁移后 `messages` 2088 条，把旧 WAL 合并进副本一读是 2118 条。差 30 条。

修法：停服务 → 把旧 `-wal`/`-shm` 和主库拷到临时目录 → `PRAGMA wal_checkpoint(TRUNCATE)`
合并 → 用合并后的库替换 → 删掉两组 `-wal`/`-shm` → 启动。实测 2118 条恢复。

教训：**迁移 SQLite 库之前先 `PRAGMA wal_checkpoint(TRUNCATE)`**（或先正常关连接），
主库和 `-wal`/`-shm` 要么一起搬、要么先合并成一个文件。只 `mv` 主库 = 静默丢数据，
而且行数只差几十条，很容易当成"本来就这样"。
