# StS2 文本桥（给 agent 的施工说明）

给 Slay the Spire 2 写一个不改变玩法的 mod：把游戏状态变成文本文件，把文本指令变成游戏动作。
用途是取代"宝可梦测试"——让 agent 通过文本玩一局，评估长任务能力。

## 环境事实（已验证，别重新摸索）

| 项 | 值 |
|---|---|
| 游戏 | `~/Games/steamLib/steamapps/common/Slay the Spire 2` |
| 引擎 | Godot 4.5.1（.NET 版） |
| 运行时 DLL | `<游戏>/data_sts2_linuxbsd_x86_64/sts2.dll`（11MB，编译时必须复制到项目根） |
| **自己写的 mod** | `<游戏>/mods/<ModId>/`（**目录不存在，要自己建**） |
| Steam 订阅的 mod | `~/Games/steamLib/steamapps/workshop/content/2868840/<workshopId>/<ModId>/` |
| BaseLib | `.../workshop/content/2868840/3737335127/BaseLib/`（**v3.4.8**） |
| 编译 | `dotnet build "/p:Sts2Path=..."`（**.NET SDK 10 已装，不需要 Godot 编辑器**） |
| 可编译参考 | `/tmp/STS2FirstMod`（已验证：0 错 0 警，2 秒出 DLL） |

编译要点：

```bash
cd <项目>
cp "<游戏>/data_sts2_linuxbsd_x86_64/sts2.dll" .
dotnet build "/p:Sts2Path=$HOME/Games/steamLib/steamapps/common/Slay the Spire 2"
```

产物在 `.godot/mono/temp/bin/Debug/<ModId>.dll`。

**不需要** `Godot_v4.5.1-stable_mono --build-solutions`（那是模板 build.sh 里的多余步骤）。

用 BaseLib 的话，把它的 DLL 也复制/引用进来：

```xml
<Reference Include="BaseLib">
  <HintPath>$(Sts2Path)/../workshop/content/2868840/3737335127/BaseLib/BaseLib.dll</HintPath>
</Reference>
```

（实际路径按上面表格里的绝对路径写；BaseLib 只在你确实要用它的 API 时才引。）

## 桥的协议

两个文件，固定路径：

```
状态（游戏写 → agent 读）：$XDG_DATA_HOME/slaythespire2/bridge/state.json
动作（agent 写 → 游戏读 → 游戏删除）：$XDG_DATA_HOME/slaythespire2/bridge/action.json
```

`$XDG_DATA_HOME` 默认 `~/.local/share`，**必须尊重环境变量**。

### state.json

```json
{
  "seq": 42,
  "ts": 1763300000.5,
  "scene": "combat",
  "floor": 3,
  "player": {
    "hp": 70, "max_hp": 80, "gold": 99,
    "energy": 3, "max_energy": 3, "block": 5,
    "powers": ["Strength 2", "Vulnerable 1"]
  },
  "hand": [
    {"index": 0, "id": "Strike", "name": "打击", "cost": 1, "playable": true, "needs_target": true, "desc": "造成 6 点伤害。"}
  ],
  "enemies": [
    {"index": 0, "id": "JawWorm", "name": "大颚虫", "hp": 44, "max_hp": 44, "block": 0,
     "intent": "攻击 11", "powers": []}
  ],
  "relics": ["Burning Blood"],
  "potions": [{"index": 0, "id": "FirePotion", "name": "火焰药水", "usable": true}],
  "available_actions": [
    {"id": "play",  "card": 0, "target": 0},
    {"id": "play",  "card": 1, "target": null},
    {"id": "potion","potion": 0, "target": null},
    {"id": "end_turn"}
  ],
  "log": ["第 3 层：遇到 大颚虫", "打出 打击，造成 6 点伤害"]
}
```

`available_actions` 是协议的核心：游戏负责枚举"当前合法且可能成功的动作"，agent 只能从里面挑。
不在列表里的动作一律拒绝，写进 `state.json` 的 `last_error`，游戏不执行。

### scene 取值

| scene | 出现时机 | 额外字段 | 动作 |
|---|---|---|---|
| `menu` | 主菜单（游戏启动后的第一屏） | `items: [{index, id, label, enabled}]`（新游戏/继续/设置/退出） | `menu_click` |
| `char_select` | 选角色 | `characters: [{index, id, name, locked}]` | `pick_character` |
| `combat` | 战斗中 | `hand` / `enemies` | `play` / `potion` / `end_turn` |
| `reward` | 战斗后奖励 | `rewards: [{index, kind, name, desc}]` | `choose_reward` / `skip_rewards` |
| `map` | 选路 | `nodes: [{index, row, kind, leads_to}]` | `choose_node` |
| `event` | 事件 | `event: {id, title, body}` / `options: [{index, text, disabled}]` | `choose_option` |
| `rest` | 篝火 | `options` | `choose_option` |
| `shop` | 商店 | `items: [{index, name, price, kind}]` | `buy` / `leave` |
| `card_select` | 选牌（升级、移除等） | `cards: [{index, id, name, upgraded}]` / `prompt` / `can_skip` | `select_card` / `cancel` |
| `game_over` | 结束 | `result: "victory"/"defeat"` / `score` | — |

**从启动到进第一层，至少要经过 `menu → char_select → map` 三步**——所以 `menu` / `char_select` 必须实现，
否则 agent 连游戏都进不去（游戏没有跳过主菜单的命令行参数）。

`settings`、`credits` 这类纯粹给人看的界面不用输出，`scene` 写 `"idle"` 并给一个 `{"id":"back"}` 动作就够。

### action.json

```json
{"action": "play", "card": 0, "target": 0}
```

规则：

1. 只接受 `available_actions` 里的内容，字段完全一致（编号从 0 开始）。
2. 处理完立刻删除 `action.json`（或改名 `.done`），避免重复执行。
3. 动作无效：不执行、清空、写 `last_error`。
4. 状态变化时才重写 `state.json`（`seq` 自增），不要每帧写。

## 启动与接管流程

```bash
cd "<游戏>"
./launch_vulkan.sh          # 或 --rendering-driver opengl（A770 上 Vulkan 有问题时）
```

游戏起来后停在主菜单。agent 的完整路径：

```
menu（新游戏）→ char_select（选角色）→ map（选第一个节点）→ combat → reward → map → ...
```

**不要假设 agent 能跳步**：主菜单之外，选角色、开局确认、地图选路都得走桥。


## 触发策略

三层，各管一件事：

**① 事件驱动（主路径）**
出牌、怪物行动、场景切换在 StS2 里都有事件，状态一变就写 `state.json`。正常流程不靠轮询猜。

**② 10 秒兜底**
只在"长时间没有任何事件"时再写一次。这是防死锁的被动保险，不是主路径。

**③ 新界面截图**
不靠数据判断界面有没有变——**桥认为是新界面就截一张**。

判定"新界面"只看这三样：

```
scene != lastScene || overlay != lastOverlay || modal != lastModal
```

不做"同一界面只截一次"的去重。理由：

- 多截几张的代价可以接受（重复的图会被历史剥离逻辑挡掉，最多占一张）；
- 但**被一个界面拦住、截图却被拦截**，这个代价不可接受——agent 会永远不知道屏幕上是什么。

所以宁可多截，不可漏截。特别是这三种情况必须截到：

| 情况 | 为什么 |
|---|---|
| 战斗中新手指引弹窗 | 底层数据一点没变（手牌还是 5 张），只有 `modal` 变了 |
| 从没见过的 overlay | 桥没有对应的 scene 处理 |
| 层间过渡 / Boss 死亡动画后的新界面 | scene 变了但数据可能还没更新 |

数据变化（出牌、掉血、能量减少）**不触发截图**——那些 state.json 里都有，agent 不需要看图。

## 实现要点

1. **先摸 API 再写**：这个游戏是抢先体验版，API 会变。先看 `/tmp/STS2FirstMod` 的示例，再用编译器报错反推类名。
2. **写文件记得 flush**（Godot 的 `FileAccess` 或 `System.IO` 都行）。
3. **轮询用定时器**（100ms 一次足够），不要 `while` 死等阻塞主线程。
4. **异常一律 catch**：宁可这一回合不执行，也不能让游戏崩。
5. **可开关**：环境变量（如 `STS2_BRIDGE=1`）控制，默认关闭。

## 交付物

```
<ModId>/
  <ModId>.json          # 清单，id 建议 sts2textbridge
  <ModId>.csproj
  src/
    Bridge.cs           # 状态采集 + 动作执行
    StateModel.cs       # 序列化结构
  README.md             # 怎么装、怎么开、路径在哪
```

## 第一版的做法：宁可多列

**第一版先把"当前屏幕上所有可点的东西"都列进 `available_actions`**，不要事先判断哪些有用。

原因：你没法在写代码的时候知道某个界面有几屏、有哪些按钮。`char_select` 可能不止一屏
（选角色 → 进阶/难度 → 确认），`event` 可能有嵌套页。如果按"我以为该有几个"实现，
agent 会卡在第二屏——而那个界面你看不到，只能对着 `state.json` 猜。

**多列不花钱，少列会卡死。** 跑一遍完整流程之后，再回头看哪些动作该删、哪些该合并。

## 本次测试的豁免

这一轮是**把桥做出来**，不是评测。所以：

- kb 是测试库（`~/.data/.kb-test`），写入**不需要人工确认**，直接记；
- 记录质量这轮不考核（写多了、写散了都没关系），下轮评测前再清；
- 但**桥的协议不要改**——schema 变了后面所有评测数据就不可比了。要改先问。

## 验收标准

A. **能编译、能加载**：游戏 mod 列表里能看到，日志有一行 "bridge ready"。
B. **状态能读**：`cat state.json | jq .` 看到 `scene` / `player.hp` / `hand` / `available_actions`。
C. **动作能执行**：写 `{"action":"play","card":0,"target":0}`，牌真的打出去，`seq` 变了。
D. **非法动作被拒**：写 `{"action":"play","card":99}`，游戏不动、有 `last_error`、不崩。
E. **全流程**：从**启动游戏**开始，agent 只通过桥把流程走到第一场战斗结束——
   至少经过 `menu → char_select → map → combat → reward` 五种 scene，中间不需要人碰鼠标键盘。

这条是真正的验收标准：**没有任何一步需要人工介入**。

## 约束

- 不修改游戏平衡（不加卡、不加遗物）；
- 只写桥的两个文件，不碰存档；
- 不复制游戏资源；
- API 不确定就先写探针打印，不要猜；
- 遇到只能由人决定的事（比如要不要加 BaseLib 依赖），记下来问，不要自己定。

## 为什么让游戏枚举动作

宝可梦那次的问题是模型**自己推断"我能做什么"**，经常推断错（点不存在的按钮），然后陷入循环。
StS2 的合法动作是有限、可枚举的——让游戏枚举，agent 只做选择，出错空间就只剩"选得不好"，而不是"点了不存在的牌"。

## 为什么用文件而不是 HTTP

Godot 没有现成 HTTP server，自己写要处理 socket 和协议解析，容易翻车。
文件轮询用 `FileAccess` 两行搞定，agent 那边 `cat | jq` 就能读，出问题时 `ls -l` 就能看状态。
100ms 轮询对回合制游戏完全够。

## 探测工具（开发期用，不是运行路径）

桥卡在某个界面时，用这套查。**桥的运行时永远不依赖截图和坐标点击**——
截图和模拟点击只是用来"发现"这个界面上有什么、点哪个有用，发现完就固化进桥。

### 什么时候用

只在"桥不认识这个界面"或"某个动作点了没反应"时用。正常推进不截图、不模拟点击。

### 怎么查

1. 截图，看屏幕上实际是什么（不要靠猜）
2. 让桥把"当前所有可点节点"的**坐标 + 标签 + 类型**列出来（给开发者看，不是给 agent 点）
3. 用模拟点击在那个坐标上**真点一次**
4. 对比点击前后的 state.json 和节点树，确认"哪个节点、什么方式"能推进
5. 把结论固化成桥的语义动作（以后这个界面就走文本，不再截图）

### 模拟点击的写法

```csharp
var pos = button.GetGlobalRect().GetCenter();
Input.WarpMouse(pos);
Input.ParseInputEvent(new InputEventMouseButton {
    ButtonIndex = MouseButton.Left, Pressed = true,  Position = pos, GlobalPosition = pos });
Input.ParseInputEvent(new InputEventMouseButton {
    ButtonIndex = MouseButton.Left, Pressed = false, Position = pos, GlobalPosition = pos });
```

走的是真实输入路径，按钮收到的和玩家点的一模一样——前置判断、tween、状态机都会正常走。

### 用它诊断"点了没反应"

两种可能，模拟点击能区分：

| 模拟点击的结果 | 说明 | 修法 |
|---|---|---|
| 点通了 | 节点对，但**直接调 API 绕过了前置判断** | 桥里改成注入事件，或者找到正确的入口方法 |
| 也不通 | 节点错，或按钮有前置条件（奖励没领完不让走） | 先补前置，再点 |

### 奖励界面的正确语义（常见误区）

StS2 战斗后**不是直接回地图**：

```
战斗结束 → 奖励列表（金币/卡牌/药水/遗物，每项可领可跳过）
        → 点卡牌 → 选牌界面（三选一 + 跳过）
        → 选/跳过 → 回到奖励列表
        → 全部处理完 → 点「前进」→ 回到地图
        → 地图上点节点 → 下一个房间
```

所以 `proceed` 是"离开奖励列表"，不是"进入地图"。两者别混。
