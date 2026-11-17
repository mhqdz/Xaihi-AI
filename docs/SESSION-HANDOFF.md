# 会话交接（2026-11-10 上下文压缩前）

> 给"压缩后的新会话"看的。读完这份就能接着干，不需要翻前面的对话。
> 注意：`docs/HANDOFF.md` 是 handoff（交接与唤醒）**功能**的文档，不是这份。

## 一、项目当前状态

| 项 | 值 |
|---|---|
| 目录 | `~/Workspace/Xaihi-AI`（2026-11-10 从 `agentd` 改名） |
| Go module | `xaihi` |
| 二进制 | `~/.local/bin/xaihi`（旧 `agentd` 已删） |
| 服务 | `xaihi.service`（systemd --user）；监听地址在 `~/.config/xaihi/env` 的 `XAIHI_ADDR`（unit 用 `EnvironmentFile=` 引它，改端口不用改 unit） |
| 数据目录 | `~/.data/xaihi/`（xaihi.db、profiles.json、endpoints.json、settings.json、tools.json、memory/、hooks/、shots/、watch.db、pixel-base/） |
| 前端 | `web/`（Vite + React），`cd web && npm run build` → `web/dist`；服务读 `XAIHI_WEB_DIR` |
| Vite dev | `127.0.0.1:5173`（用户自己起的；改名前它固化了旧路径导致 404，用户已重启） |
| git remote | `git@github.com:mhqdz/Xaihi-AI.git`（GitHub 仓库已改名，旧名 301 重定向） |
| SSH | `~/.ssh/id_ed25519_kb`（有 RW，push 正常） |
| gh CLI | 已装 2.103.3；**token 失效**，需要用户跑 `gh auth refresh -h github.com` |

**数据库不变式**（判断有没有改坏）：

```bash
sqlite3 ~/.data/xaihi/xaihi.db "select (select count(*) from conversations),(select count(*) from tasks),(select count(*) from outbox),(select count(*) from messages where conv_id=51);"
# 现在：21|0|0|575   （messages 总条数 2118）
```

## 二、工作区有未提交改动（接手先看）

```
 M internal/tools/tools.go
?? internal/toolmeta/
```

这是**做了一半的"工具分类"**（见第五节），先别丢。

## 三、已完成的事

1. **审计与配置重构**：`docs/AUDIT.md`；三层配置（接口→模型→配置）撤成两层（endpoints → profiles）；模型能力必填、`auto_caps` 运行时跟随上游、`model-cache.json` 派生缓存。
2. **阅读栏宽度**：去掉写死的 1400 上限 → 跟舞台宽度；修掉 resize 里的"棘轮"（窗口缩一次列宽就永久变窄）；列宽铺满时右侧标尺让位。
3. **配置卡文案/排版**：说明行统一 `.field-hint`、每字段最多一行、删掉解释腔；正文的 640px（endfield `--ef-measure`）放开；卡片内边距随列宽 13.6→36。
4. **kb 重构**（`~/Workspace/KnowledgeBase`）：1512 行单文件 → 12 文件 → 抽独立 module `kbapp/core`；中文检索 2-gram（词序无关）、`--category` 前缀保留、空目录提示。xaihi 用 `replace kbapp/core => ../KnowledgeBase/core` 引用它的 `entry.ParseFM`。
5. **记忆层** `internal/memory`：文件式、**全局**、预算 6000 token，`memory_save/forget/list`，注入 system prompt（挑选从新到旧、拼装升序以免打散 KV cache）。
6. **观察器** `internal/watch`：`hyprctl activewindow/clients` 采样、30 秒去抖、事件进 `watch.db`（不进记忆）、窗口消失回调。
7. **触发器** `internal/hook`：`hooks/*.json` 定义（无"什么时候"字段）、配额 10/天、冷却 3 秒、**只挂 unix socket**（`~/.data/xaihi/xaihi.sock`，TCP 上 404）、CLI 注册（`xaihi hook add/list/rm`）、5 秒热重载（目录指纹轮询）、`xaihi trigger`。
8. **前端左栏四块**：`Xaihi/16 天 | 会话 | 归档 | 本轮`，不可选中复制；第一块是门面，没有观察会话时点它弹引导页。
9. **改名**：module/二进制/服务/数据目录/DB/socket/环境变量(`XAIHI_*`)/文档/UI 全改。
10. **WAL 事故已修**：改名时只 `mv` 主库、没带 `-wal/-shm`，丢了 30 条；已 checkpoint 合并回来。详见 `docs/REPAIR.md` 最后一条。

## 四、用户的决定（不要推翻）

- 正式名 **Xaihi-AI**；数据目录也改名了。
- **记忆是全局的**（跨项目累积），不按项目。
- **Xaihi = profile 的人格（`profile.System`）+ 全局记忆**；"会话只是跟她说话的窗口"。所以"只允许一个观察会话"的特殊性**要去掉**（尚未做）。
  人格**不是**全局一份，而是跟着 profile 走（同一 profile 下的所有会话共用）。**不拆成独立文件**（2026-11-10 用户确认）。
- 持久化会话作为 **profile 的可选项**：打开后才能选记忆/钩子工具。
- 图像类工具按**模型能力**判断（`profile.input` 含 image 才给 `screenshot`/`view_image`）。
- 触发器：**注册走 CLI**（本地 shell 权限）、**触发走 unix socket**（不开 TCP）、钩子文件由 agentd 维护（人/agent 不手写）。
- 观察器：L3 视觉但**只事件触发**、不设定时间隔、截图配额 300/天。
- 冷却 3 秒、配额 10/天（测试值）；**不做深度限制**（配额兜底）。
- 钩子产出**倾向写回创建它的会话**（选项 A，尚未实现）。
- profile 可编辑，mem 可选，人格文件先不管（按需选择/拷贝）。

## 五之前、2026-11-10 深夜：陪伴型三块（时间感 / 待办 / 日记）+ 人格可编辑

- `internal/clock`：`[现在]`/`[今天]`/`[上次对话]` 三行，注入在人格之后、记忆之前
- `internal/todo`：`todos.json`（上限 50、原子写）+ `todo_add/list/done` + 未完成注入（≤20）
- `internal/journal`：`journal/<日期>.md`（一天一个、≤500 字、不注入、只给路径）+ `journal_write`
- 人格可编辑：`GET/PUT /api/personas/<名字>` + 前端 EditorCard 复用
- `personas/xaihi.md` 写成了终末地干员赛希（称用户「管理员」）
- 删了 `ds-pro` 配置（模型已下线）
- 实测：todo_add 落盘 → 新会话问"有什么待办"能答出；journal_write 写出当天日记，
  内容里引用了时间感的"5h12m / 82 次切换"

## 五、已完成：人格可插拔 + 工具分类 + 钩子工具

**A 人格可插拔**（已完成）
- `config/personas.go`：`LoadPersona/ListPersonas/SavePersona`，目录 `<data>/personas/<名字>.md`
- `Profile.Persona` + `Profile.Persistent` 字段
- `agent.New` 把人格拼在 `System` 前（`estimateChars`/`guardSystem` 看到的是最终内容）
- 记忆只在 `Persistent=true` 时注入（两处拼装点）
- 校验：人格必须存在；memory/hook 工具要 persistent；image 工具要 input 含 image；
  persistent 必须有人格
- 前端：配置卡「人格与持久化」段（下拉 + 预览 + 开关），关开关时自动摘掉依赖它的工具

**B 工具分类**（已完成）
- `/api/tools` 带 `category`/`needs`；前端按 core/memory/hook/image 四组渲染，不满足条件的**灰掉**
- `/api/personas` 列表接口

**C 钩子工具**（已完成）
- `hook_add/hook_list/hook_rm`，复用 `internal/hook` 的 Write/Remove/LoadDir
- `hook.Def.ConvID`：工具创建时绑当前会话，触发写回；没绑/会话没了 → 收件箱
- 单测：`TestHookTools`（落盘、绑会话、list、rm、非法名字）

**D 收口**（已完成）
- 触发写回绑定会话（`convExists` 兜底）
- 像素基线已重拍，`pixel_diff.py compare` 6/6

## 五之二、旧的"工具分类（一半）"记录（已被上面取代）

已完成：

- `internal/toolmeta/toolmeta.go`：`Core/Memory/Hook/Image` 四类 + `Category(name)` / `NeedsVision` / `NeedsPersistent` / `NeedsPrompt`
- `tools.Spec` 新增 `Category`、`Needs []string`；内置工具打了标签（memory_* → memory；screenshot/view_image → image + `Needs:["vision"]`）；`Registry()` 里没标签的默认 core

**还没做**：

1. `config.Profile` 加 `Persistent bool \`json:"persistent,omitempty"\``
2. `ValidateProfileWith` 校验：
   - `tools` 含 memory_*/hook_* 且 `!Persistent` → 「这些工具需要先打开持久化会话」
   - `tools` 含 image 类且 `input` 不含 `image` → 「模型不能接收图像」
3. 前端（`web/src/components/Settings/ModelForm.jsx`、`form.jsx`）：
   - 工具列表**按分类分组**（core / memory / hook / image）
   - `memory`/`hook` 组在 `persistent` 关时禁用；`image` 组在 `input` 不含 image 时禁用
   - 配置卡加「持久化会话」开关
4. `hook_*` 工具（用户已同意）：`hook_add/hook_list/hook_rm`，复用 `internal/hook` 的 `Write/Remove/LoadDir`
5. 钩子绑定会话：`hook.Def` 加 `ConvID`，触发写回该会话；会话不存在则写收件箱
6. 去掉"唯一观察会话"的特殊性（`web/trigger.go` 的 `watchConv()`）

## 六、已知的坑（别再踩）

| 坑 | 说明 |
|---|---|
| SQLite WAL | 迁移/改名时主库和 `-wal`/`-shm` 要么一起搬、要么先 `PRAGMA wal_checkpoint(TRUNCATE)`。只 `mv` 主库会静默丢几十条 |
| Vite 绝对路径 | 启动时固化 root；目录改名后 dev server 返回 404，必须重启 |
| `version` | 是 `var`（不是 const），构建要带 `-ldflags "-X main.version=$(git rev-parse --short HEAD)"` |
| `/api/convs` 字段 | 返回**大驼峰** `ID/Title/N/UpdatedAt/ParentID/Kind/Seq`，不是小写 json tag |
| Store 接口 | 往 `store.Store` 加方法后 `JSONStore` 也要实现，否则 `internal/web` 测试编译失败 |
| 时间字段 | `messages.created_at` 是 TEXT，`modernc` 驱动不会自动转 `time.Time`，要扫 string 再 `time.Parse`（多格式） |
| pixel 基线 | `~/.data/xaihi/pixel-base` 是改名前的，UI 也变了 → `pixel_diff.py compare` 会全红，需要重拍 |
| runTurn 是同步的 | `/api/trigger` 必须异步（内部 goroutine），否则 curl 被模型卡几分钟，冷却也失效 |
| 测试数据清理 | 观察会话/触发产生的会话会污染不变式，测完删（`delete from messages where conv_id=…` + `conversations`） |

## 七、常用命令

```bash
export PATH=/usr/local/go/bin:$PATH
cd ~/Workspace/Xaihi-AI
go test ./...                                    # 10 个包
go build -ldflags "-X main.version=$(git rev-parse --short HEAD)" -o ~/.local/bin/xaihi ./cmd/xaihi
systemctl --user restart xaihi
cd web && npm run build                          # 前端产物 → web/dist

xaihi hook list
xaihi hook add game-exit --prompt "…" --quota 10 --cooldown 3
xaihi trigger game-exit --data '{"app":"steam"}'
journalctl --user -u xaihi -f

# UI 端到端测试（需要 geckodriver 127.0.0.1:4444）
cd scripts/ui-test && python3 check_config.py
```

## 八、关键文件

| 文件 | 作用 |
|---|---|
| `internal/memory/memory.go` | 记忆：front matter、预算挑选、Render 注入 |
| `internal/watch/watch.go` | 观察器：hyprctl 采样、去抖、事件表、截图配额 |
| `internal/hook/hook.go` | 触发器：加载/配额/冷却/Write/Remove/LoadDir/热重载 |
| `internal/toolmeta/toolmeta.go` | **新增**：工具分类与能力要求 |
| `internal/tools/tools.go` | 工具注册（Spec、内置工具、memory_* 实现） |
| `internal/config/profiles.go` | Profile 结构 + ValidateProfileWith（校验加这里） |
| `internal/web/web.go` | 路由、`/api/health`（含 `first_at`） |
| `internal/web/trigger.go` | 触发 mux（unix socket）、`watchConv()`、`NewHooks` |
| `cmd/xaihi/main.go` | CLI：serve/chat/hook/trigger/convs/… |
| `web/src/App.jsx` | 左栏四块、引导页、会话列表（注意大驼峰字段） |
| `web/src/components/Settings/ModelForm.jsx` | 配置卡（要加"持久化会话"开关） |
| `web/src/components/Settings/form.jsx` | Field/SwitchRow/TagInput 等表单件 |
| `docs/API.md` | 接口文档（含"触发器：本地 socket"一节） |
| `docs/REPAIR.md` | 踩坑记录（WAL 那条必读） |

## 八点五、待做（2026-11-14 记下，下次做）

**前端：截图按路径渲染缩略图**
截图工具现在只返回 `/shots/xxx.jpg` 路径（看图改成显式 `view_image`），所以截完图界面上不再自动出现那张图。
要在前端把消息里出现的 `/shots/…` 路径渲染成缩略图（点击放大 / 打开原图），这样"截图后能看到"和
"模型不自动吃图"两件事就都有。落点：`web/src/components/MessageCard.jsx`（或 ToolGroup 的 tool 结果渲染），
识别 `/shots/` 前缀 + 图片扩展名。

**llm.ChatRequest.Temperature 没接上**
`Temperature *float64` 有 json tag 但全库没有赋值点，永远 nil、永远不发出去 —— 看着支持实际不支持。
要么接上（前端给个开关 / profile 加字段），要么删掉。2026-11-15 记下，暂不做。

## 九、下一步建议顺序

1. 收尾"工具分类"（第五节 1–3）：后端校验 + 前端分组禁用 —— 用户当前最想要。
2. `hook_*` 工具 + 钩子绑会话（第五节 4–5）。
3. 去掉唯一观察会话（第五节 6）。
4. 人格拆成可插拔文件 `personas/*.md` + `profile.persona` 引用（用户 2026-11-10 改主意：为了
   "插件式人格 + 共同记忆"）。拼装顺序改为 人格 → 记忆 → KV → 摘要（`loop.go`），
   记忆只在 persistent 开启时注入。**尚未实现**。
5. 重拍像素基线。

## 十、2026-11-17 凌晨：StS2 文本桥（未完成，明天继续）

**目标**：给 Slay the Spire 2 写一个文本桥 mod，让 agent 通过 state.json / action.json 玩一局，
用来取代"宝可梦测试"评估长任务能力。**这是评测工具，不是产品功能。**

**位置**：`plugins/sts2textbridge/`（5 个源文件 2055 行 + README 210 行，已提交 4ad4653）

**能跑通的**：`menu → char_select → loading → combat → card_reward → reward`
真打过一场：HP 80→54、金币 99→115、手牌/敌人/意图/动作白名单都采到了。

**没验证的**：`reward → map`。`proceed` 动作是最后才加的，`Proceed()` 一次都没跑过。

**明天要做**：
1. 实测 `reward → map`（`{"id":"proceed"}`）
2. 补它还没遇到的界面。已知至少还有：宝箱、商店、篝火、问号事件、Boss 遗物选择、
   阵营/进阶选择、以及事件里的多页嵌套
3. 跑通之后再谈"用它评测通过率"

**环境**（已验证，别再摸索）：见 `docs/STS2-BRIDGE.md`；BaseLib 在
`~/Games/steamLib/steamapps/workshop/content/2868840/3737335127/BaseLib/`（v3.4.8），
自己写的 mod 放 `<游戏>/mods/`，编译只要 `dotnet build`（不需要 Godot 编辑器），
但**必须先把 `data_sts2_linuxbsd_x86_64/sts2.dll` 复制到工程根**。

**教训**：这一轮 agentd 的压缩逻辑被这个长任务逼出五个 bug（图片累积、压缩没提 recall、
预算没预留 system、compact 不算 tool_calls、view_image 不压图），已全部修复。
其中"compact 不算 tool_calls" 是真根因——工具轮的参数才是上下文的大头。

**注意**：测试期间的 Steam Cloud 目录已恢复（`remote` 104 个 history）。
`remote.bak-xaihi` / `remote.before-restore` 两个备份还在，确认无用后可删。

### 2026-11-17 下午续：进展与断点

**新增能力**（已提交）：

- 触发策略按文档实现：事件驱动 + 10 秒兜底 + 新界面（scene/overlay/modal 变化）必截图
- `NProceedButton` 改用 `OnProceedButtonPressed()`（`ForceClick` 对它无效）
- 战斗：`CardCmd.AutoPlay(new BlockingPlayerChoiceContext(), card, target)` 出牌，
  不再走 `TryManualPlay`（那条路要 UI holder，桥拿不到，会报 "card holder passed is null"）
- reward 界面能列奖励项、`take_reward` 能领、`proceed` 能点

**已验证走通**：`menu → char_select → loading → combat → card_reward → reward`

**当前断点**：`reward → map`。`proceed` 点了之后 state 还在 reward（点击有反应但不进地图）。
试过 ForceClick、OnProceedButtonPressed、等 1.5 秒，都没过去。
`card_reward` 之后也停在奖励循环里。

**没验证的界面**：map、shop、rest、event、宝箱、Boss 遗物、层间过渡、通关后场景。

**下次建议**：
1. 先单独攻 `reward → map`：在游戏里手动点一次「前进」，同时让桥记录点击前后的节点树差异，
   看游戏自己是怎么完成这个转换的（重点看 `NRun` 的 scene 容器换没换、有没有过渡动画节点）
2. 通了之后再往下补界面
3. 桥已经能自己写 `bot.py` 自动打，评测脚本这块不用重新造

### 2026-11-18 凌晨：两个大突破

**通了**（这是之前一直卡的）：

1. **`reward → map`**：根因是奖励界面关掉后，`NRewardsScreen` 节点还留在 `NOverlayStack` 里，
   但 `Visible` 仍报 True（淡出动画做完了节点没删）。判定要加 `Modulate.A` / `SelfModulate.A`，
   alpha 接近 0 就当它不存在。
2. **商店**：`items` 一直是 0，因为商店屋里没有带名字的按钮，只有一排 `Hitbox`，
   `LabelOf` 拼不出内容。改成按槽位读 `NMerchantSlot.Entry`。
3. **敌人采集（关键）**：`enemies` 一直是 0，但数据层明明有 3 个双尾鼠。
   根因是 **index 错位**：`FillCombat` 用 `Enemies.Count` 当编号，死掉的敌人被 `continue` 跳过，
   后面全部错位。改成 `for i in cs.Enemies` 直接用下标做 `index`。
   数据源：`CombatManager.Instance.DebugOnlyGetState() -> CombatState`，`cs.Enemies` 是纯数据。

**完整链路（已验证）**：
`menu → char_select → loading → combat → card_reward → reward → map → shop → map → combat`

**当前卡点**：战斗里 3 个敌人全死之后，`enemies` 变成空、但 `scene` 还是 `combat`，
不自动结算成 reward。试过 `end_turn` 没验证（那一轮没执行）。

**下次从这里接**：
```
敌人全死 → cs.Enemies 为空
  → 查 CombatManager.Instance.IsOver / IsInProgress
  → 如果 IsOver，scene 应该直接判 reward
  → 如果没 IsOver，说明还差一次 end_turn 或一次 tick
```

**环境备份**：`/tmp/sts2-src`（反编译源码，29MB）、`/tmp/sts2-eval*/`（三个测试存档目录）。
存档 3（profile3）是后期存档，floor 8 起，适合快速测后期界面。

### 2026-11-18 凌晨：上下文计价的三个根本错误（已修）

这一轮没有推进桥的界面，全部时间在修 agentd 的上下文管理。**桥本身没退步**。

**① `max_prompt_tokens` 是 None → 吃了默认值 105,000**

`defaultMaxPromptTokens = 105000`（按 131K 窗口的 80% 设的），而 `deepseek-v4.1-flash` 是 1M 窗口。
等于用 131K 模型的参数跑 1M 模型，**只用了一成的能力**。
→ 已给 `Xaihi` / `ds-opencode` 两个 profile 设成 900,000。

**② `charsPerToken = 1.8`，DSH 用的是 4**

DSH 的 `dsh-token-meter` 里 `CHARS_PER_TOKEN = 4`。1.8 是中英混合经验值，
对代码/英文/base64 都高估 2.2 倍。→ 已改成 4。

**③ 图片按 base64 长度计价（错得最离谱）**

一张 150KB 截图 → base64 280KB → 按 1.8 折算成 15.5 万"token"，
而 API 实际只收约 1000 token（按分辨率算固定档位，和字节数无关）。
DSH 的处理（`dsh-compaction-image-offload` + token-meter 的 image 分支）：
**只算引用结构的 JSON 长度，不算 base64**。
→ 已改成固定 `imageTokenCost = 1000` token/张。

**三个错误叠加的后果**：一张图占预算 17% → 每轮必触发 compact（1935 次）
→ 历史被砍到 70 条 → 模型失忆 → 重读源码 → 工具调用暴涨。

**④ 压缩改成 surface 缓存（对应 DSH 的 `replaceGeneration`）**

原来每轮都从完整历史（4644 条 / 280 万字符）重算一遍、发完又丢掉。
现在缓存上次算好的 surface，只把新增消息接上去；历史被改写（seq 不连续）
或预算变了才全量重算。
**数据一层完全不动**（`Save` 本来就是只追加），`recall` 照样能搜到被省略的部分。
→ 压缩从 1935 次降到「重启后首轮 1 次」。

**验证数据**：
```
compact 次数        1935 → 1
数据库消息数        4647（一条没少）
surface             1439 条 / 82 万字符（预算 198 万）
② 之后的日志        02:29:10 起无 compact
```

**下次从这里接**：桥卡在「3 个敌人全死但 scene 还是 combat」。
只需要打印四个值：`CombatManager.Instance.IsOver` / `IsInProgress` / `IsOverOrEnding`、
`cs.Enemies.Count`。`IsOver=true` 就是 scene 判定漏了。
