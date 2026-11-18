# StS2 文本桥（sts2textbridge）

给 Slay the Spire 2 写的一个不改玩法的 mod。它做两件事：

1. 把游戏当前状态写成文本（`state.json`）——场景、血量、手牌、敌人、当前合法动作；
2. 读一个文本文件（`action.json`）当指令，代替鼠标点击。

用途是让 agent 只靠读写文件就能玩一局，用来测长任务能力。玩法数据只读，不加卡、不改数值。

上游设计说明（为什么要枚举动作、为什么用文件而不是 HTTP）见 `~/Workspace/Xaihi-AI/docs/STS2-BRIDGE.md`。
本 README 只讲怎么跑起来和协议长什么样。

## 环境

| 项 | 值 |
|---|---|
| 游戏 | `~/Games/steamLib/steamapps/common/Slay the Spire 2`（可用 `STS2_GAME` 覆盖） |
| 引擎 | Godot 4.5.1（.NET 版） |
| 编译 | .NET SDK 9+，`Godot.NET.Sdk/4.5.1`。**不需要 Godot 编辑器**，不需要 `--build-solutions` |
| 引用 | 直接引用游戏自带的 `<游戏>/data_sts2_linuxbsd_x86_64/sts2.dll`，不用复制进项目 |
| 安装位置 | `<游戏>/mods/sts2textbridge/`（该目录原本不存在，脚本会建） |

## 编译与安装

```bash
./build.sh
```

它做的事：`dotnet build "/p:Sts2Path=$GAME"`，然后把 `.godot/mono/temp/bin/Debug/sts2textbridge.dll`
和 `sts2textbridge.json` 一起拷到 `<游戏>/mods/sts2textbridge/`。

产物路径固定为 `.godot/mono/temp/bin/Debug/<ModId>.dll`，换 Debug/Release 时要注意脚本里的路径。

> 踩过的坑：`build.sh` 里如果给 `dotnet build` 接了管道（`| tail`），务必先 `set -o pipefail`，
> 否则编译失败会被 `tail` 吞掉，你会拿着旧 DLL 测半天，还以为是逻辑问题。

## 启动

```bash
./run.sh                  # 用真实存档启动（appid 2868840）
./run.sh /tmp/sts2-test   # 用隔离的数据目录启动（干净存档）
./run.sh /tmp/sts2-test --restart   # 先杀掉旧进程
```

`run.sh` 会设：

- `SteamAppId=2868840`（真实 appid，可被 `STS2_APPID` 覆盖）
- `STS2_BRIDGE=1`（不开这个，mod 加载但什么都不做）
- `XDG_DATA_HOME=<给的目录>`（给了的话）
- 用 `--rendering-driver opengl3` 启动，日志在 `/tmp/sts2-run.log`

**安全阀**：如果 `STS2_APPID` 被改成了别的值却没给数据目录，`run.sh` 直接退出（exit 2）。
原因是换 appid 会切到另一套 Steam 云存档空间，但 XDG_DATA_HOME 还指着真实目录时，
云会把旧存档同步下来覆盖本地。2026-11-17 这样丢过一次 run history。

其它环境变量：

| 变量 | 作用 |
|---|---|
| `STS2_BRIDGE=1` | 总开关，不设则 mod 只打一行 "disabled" 就退出 |
| `STS2_BRIDGE_DATA` | 直接指定桥的文件目录（优先于 XDG_DATA_HOME） |
| `STS2_BRIDGE_TRACE=1` | 每个阶段往 `trace.log` 写一行，用来定位原生崩溃点 |
| `STS2_BRIDGE_QUIET=1` | 不轮询界面，只在收到动作时读一次状态 |

## 协议

两个文件，固定路径：

```
状态（游戏写 → agent 读）：$STS2_BRIDGE_DATA/state.json
动作（agent 写 → 游戏读 → 游戏删）：$STS2_BRIDGE_DATA/action.json
```

目录解析顺序：`$STS2_BRIDGE_DATA` → `$XDG_DATA_HOME/slaythespire2/bridge` → `~/.local/share/slaythespire2/bridge`。

轮询 100ms。只有状态签名变化时才重写 `state.json`（`seq` 自增），不是每帧写。

### state.json

```json
{
  "seq": 8,
  "ts": 1794856931.18,
  "scene": "reward",
  "floor": 1,
  "player": {"hp": 54, "max_hp": 80, "gold": 115, "energy": 0, "max_energy": 3, "block": 0},
  "relics": ["燃烧之血"],
  "rewards": [{"index": 0, "kind": "NRewardButton", "name": "将一张牌添加到你的牌组。"}],
  "prompt": "选一项奖励，或继续",
  "clickables": [{"index": 0, "label": "将一张牌添加到你的牌组。", "kind": "NRewardButton"}],
  "available_actions": [
    {"id": "take_reward", "index": 0},
    {"id": "proceed"},
    {"id": "click", "index": 0}
  ],
  "probe": {"root_scene": "NRun", "floor": 1, "act_floor": 1, "act": 1,
            "overlay": "NRewardsScreen", "clickable_count": 1}
}
```

字段（`null` 不输出）：

| 字段 | 说明 |
|---|---|
| `seq` / `ts` | 状态序号（变化才自增）/ unix 时间戳 |
| `scene` | 当前场景，见下表 |
| `floor` | 总层数 |
| `player` | `hp` `max_hp` `gold` `energy` `max_energy` `block` `powers` |
| `hand` | 战斗手牌：`index` `id` `name` `cost` `playable` `needs_target` `desc` |
| `enemies` | `index` `id` `name` `hp` `max_hp` `block` `intent` `powers` |
| `relics` / `potions` | 遗物名 / 药水 `{index,id,name,usable}` |
| `rewards` | 奖励项 `{index,kind,name,desc}` |
| `nodes` | 地图可走点 `{index,row,kind,leads_to}` |
| `items` / `characters` | 菜单项 / 角色 `{index,id,name,locked}` |
| `event` / `options` | 事件正文与选项 |
| `cards` | 选牌界面 `{index,id,name,upgraded}`，配 `prompt` / `can_skip` |
| `clickables` | 当前屏幕上**所有**可点节点（兜底，保证不会因为漏枚举而卡死） |
| `available_actions` | 当前合法动作，协议的核心 |
| `last_error` | 上一个动作被拒的原因 |
| `probe` | 调试探针：只在没见过的场景里填，用来事后反推界面结构 |

### scene

| scene | 含义 | 实测 |
|---|---|---|
| `menu` | 主菜单（含子菜单） | ✅ |
| `char_select` | 选角色 / 选难度 | ✅ |
| `loading` | 过场（`NRun` 已建但房间还没就绪，或点完「出发」正在预载）——此时没有动作可做，等 | ✅ |
| `combat` | 战斗 | ✅ |
| `reward` | 战斗后奖励界面（`NRewardsScreen`） | ✅ |
| `card_reward` | 三选一（`NCardRewardSelectionScreen`） | ✅ |
| `card_select` | 牌组选牌家族：`NDeckTransformSelectScreen` / `NDeckCardSelectScreen` / `NDeckEnchantSelectScreen` / `NDeckUpgradeSelectScreen` / `NChooseACardSelectionScreen` / `NChooseABundleSelectionScreen` / `NSimpleCardSelectScreen`。枚举 `NCardHolder`，动作 `select_card`，选完通常还要 `click` 那个 `NConfirmButton` | ✅ |
| `map` | 地图选路 | ✅ |
| `rest` | 篝火（休息 / 锻造） | ✅ |
| `shop` | 商店（商品名/价格/类型，`buy` / `leave`） | ✅ |
| `event` | 事件（选项走 `click`） | ✅ |
| `game_over` | 结束，只有 `prompt`（`result` / `score` 字段已定义但还没填） | ✅ |
| `modal` | 弹窗（是/否） | ✅（放弃存档确认框） |
| `overlay` | 其它覆盖层，只有 `prompt` + `click` | ⚠️ 兜底 |
| `idle` | 没有可操作的根场景（主菜单之前、加载中） | ✅ |
| `error` | 采集本身抛异常，`probe.error` 有栈 | — |

**还没验证的**：Boss 战、层间过渡（打完一层进下一层）、通关后的界面。

### available_actions

| 动作 | 参数 | 用在哪个 scene |
|---|---|---|
| `menu_click` | `index` | menu |
| `pick_character` | `index` | char_select |
| `start_run` | — | char_select（「出发」按钮） |
| `play_card` | `index`，可选 `target` | combat |
| `end_turn` | — | combat |
| `map_move` | `index` | map |
| `take_reward` | `index` | reward |
| `proceed` | — | reward（「继续」/跳过） |
| `choose_card` | `index` | card_reward |
| `skip_card_reward` | — | card_reward |
| `modal_yes` / `modal_no` | — | modal |
| `click` | `index` | 任何场景（点 `clickables[index]`，兜底） |
| `wait` | — | 没有其它动作时 |
| `dump_tree` | 可选 `whole` | 任何场景，把节点树写到 `dump.txt`，排障用 |

`clickables` 的 `index` 和 `click` 的 `index` 是同一套编号，从 0 开始。
`dump_tree` 不是玩法动作，只写文件，用来在没见过的界面上摸结构。

### action.json

```json
{"action": "play_card", "index": 0, "target": 0}
```

规则：

1. 协议上只该从 `available_actions` 里挑。实现里每个动作自己做下标和界面校验（越界、节点已释放、
   界面上找不到按钮……），失败就写 `last_error`、游戏不动，不会崩。
2. 读完执行完立刻删掉 `action.json`，避免重复执行；解析失败的也会删（连续 3 次才写 `last_error`）。
3. 写入要原子：先写 `action.json.tmp` 再 `mv`（`act.sh` 就是这么做的），否则游戏可能读到半个文件。
4. 一次只写一个动作，写完等 `seq` 变了再写下一个。

### 小工具

```bash
./p.sh                                  # 打印当前状态摘要（probe.py）
./p.sh '{"action":"end_turn"}'          # 发动作，等 1.5s 再打印状态
./act.sh '{"action":"click","index":0}' # 只发动作，不打印
```

`p.sh` / `act.sh` 都认 `STS2_BRIDGE_DATA`。

## 已知限制

- **`reward` → `map` 的 `proceed` 动作刚加上，未实测。** `Proceed()` 会找界面上的
  proceed/continue/skip 按钮再点它，代码路径没跑过；最后一次实测的 `state.json` 停在
  `scene=reward, seq=8`，没有之后 `map` 的记录。不要当成已验证。
- `event` / `rest` / `shop` / 通用 `card_select` 没有专门实现，只有 `prompt` + `click` 兜底，
  能用但字段很薄。
- `probe` 只在没见过的场景里填，正常场景看不到内部结构。
- 名字/描述取的是游戏当前语言（中文），换语言会变，不要拿来做匹配键，用 `id`。
- `game_over` 的 `result` / `score` 目前永远是 null，只有 `prompt`。
- 只支持单人标准局；不碰存档，也不做任何存档相关的保证。

## 目录

```
sts2textbridge.json      mod 清单（id / 版本 / has_dll）
sts2textbridge.csproj    引用 $(Sts2Path)/.../sts2.dll
project.godot            Godot 项目文件（只为让 SDK 认路径）
src/Bridge.cs            mod 入口、文件轮询、目录解析
src/StateCollector.cs    根场景判定、菜单/角色/弹窗采集、clickables
src/RunCollector.cs      一局之内：战斗/地图/奖励/选牌/结算
src/ActionExecutor.cs    动作分发与执行
src/StateModel.cs        state.json 的结构（snake_case）
build.sh / run.sh        编译安装 / 启动
probe.py / p.sh / act.sh 读写桥的小工具
```
