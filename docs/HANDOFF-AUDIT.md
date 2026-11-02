# 交接卡：agentd 自主审计（2026-11-03）

> **已被 [`docs/AUDIT.md`](AUDIT.md) 取代。** 本文件写作时 theme.css 的合并还没验证（当时那份 943 行的实验版后来丢了），
> 实际完成的所有改动、验证证据和批次记录都在 AUDIT.md。这里保留作为过程记录，别再按它开工。

> 用途：上下文压缩 / 换会话后，凭这一份即可接上工作。**不要凭记忆，先跑第一节的命令确认状态。**

---

## 0. 一句话现状

安全批已 push 两个 commit；工作区里有一份**未验证**的 `theme.css` 等价合并（1401 → 943 行），下一步就是验证它。

---

## 1. 先跑这三条确认状态

```bash
cd ~/Workspace/agentd
git log --oneline -3          # 应为 677254e / 68f61bd / 1715c85
git status --short            # 应有：M wd.py、M theme.css、?? pixel_diff.py
wc -l web/src/theme.css       # 应为 943（合并版）；若是 1401 说明已回滚
```

服务与浏览器：

```bash
systemctl --user is-active agentd          # active
curl -s http://127.0.0.1:18400/api/health  # {"ok":true,...}
```

---

## 2. 目标（goal objective 原文）

对 agentd（Go 后端 + React 前端）做分阶段自主审计与优化。

**问题分级**（按优先级修，同级按影响面排）：

- **P0** 崩溃、报错、数据不一致
- **P1** 卡顿、状态错乱、响应式破坏、同角色元素样式不统一
- **P2** 元素与当前风格不合时宜（如系统默认样式的 select）、点击目标过小不好点

**改动顺序**：严格先安全后危险，每批独立 commit + push，可单独回退

1. 死代码、测试脚手架、确定无副作用的修复
2. 样式与交互一致性
3. 后端逻辑、数据库结构、UI 结构调整

**硬约束**

- 每批改动前有复现或截图，改后有对比；**不许“应该没问题”**
- 六套测试持续全绿（见第 4 节）
- 改视觉必须过 `pixel_diff`；刻意改视觉的那批，把该页面排除在对比外并说明
- 不打印凭据、不删用户数据、测试数据自己清理

**保留用户已确认的决策**（不许“优化”掉）

- 左栏浅灰配色、Maple Mono 字体
- 标尺 8px 间距、当前轮用深色不用橙色
- 标题只显示用户原话，**不拼接**上一轮
- 分页按轮、10 轮、上 2 下 8
- 标题不压正文（宽度按 `--colw` 自适应）

**产出**：`docs/AUDIT.md` 分级问题清单（现象 / 根因 / 修复 / 验证），每批 push 后更新；最终附真机截图与测试报告。

---

## 3. 下一步：验证 theme.css 合并

### 3.1 跑对比

```bash
cd ~/Workspace/agentd
timeout 280 python3 scripts/ui-test/pixel_diff.py compare /tmp/pixel_base
```

基线 6 张在 `/tmp/pixel_base/`（**/tmp 会被清，没了就先 `baseline` 重截一份，但那样只剩“合并后”自己跟自己比，没意义——这种情况直接从 git 回滚）**。

### 3.2 结果处理

**全部一致** → 提交：

```bash
git add -A && git commit -m "theme.css: 同选择器合并（1401→943 行，像素零差异）"
git push origin main
```

**有差异** → 立刻回滚，别调：

```bash
git checkout -- web/src/theme.css
cd web && npm run build
```

然后把差异记进 `docs/AUDIT.md`，说明哪条规则不能合并（通常是 shorthand / longhand 顺序敏感，例如 `background` 与 `background-color` 交错）。

### 3.3 合并做了什么

按 `(media, 选择器)` 分组，组内按出现顺序合并属性、后者覆盖前者，输出一条规则并保留首次出现的位置。合并 86 组、删 127 条重复规则。

典型重复：`:root` 7 次、`.drawer-figs a` 6 次、`.drawer-figs a .nfwrap` 5 次、`.scale i` 4 次。

**这是“修根源”而不是打补丁**：原来是 J 视觉层追加在文件末尾靠级联覆盖，同一选择器散落多处，改一个颜色要先搞清“最后生效的是哪条”。

---

## 4. 六套测试（全绿才算完成）

```bash
cd ~/Workspace/agentd
for f in check_layout check_tools run run3 robustness; do
  echo "=== $f"; timeout 300 python3 scripts/ui-test/$f.py 2>&1 | tail -3
done
timeout 280 python3 scripts/ui-test/stream_test.py 2>&1 | tail -4
```

| 脚本 | 覆盖 |
|---|---|
| `check_layout.py` | 设置面板三级卡片的尺寸一致性、窄屏只显示一张卡 |
| `check_tools.py` | 工具组 DOM 顺序、`data-seq` 严格递增、交互无 JS 错误 |
| `run.py` | 设置宽屏三级、模型/工具卡、发送按钮状态 |
| `run3.py` | 窄屏上栏、竖屏把手、正文左边距、输入框高度 |
| `robustness.py` | 7 组黑盒：不存在的会话 / 空会话 / 10000 字 / 连点发送 / 响应式来回切 / 设置深链与 Esc / 标尺跨窗口跳转 |
| `stream_test.py` | 11 项：发送→done、停止→interrupted、kind 归类、刷新重连 |
| `pixel_diff.py` | 像素基线对比（视觉改动必跑） |

**已知的测试环境坑**：geckodriver 卡在 `Session already started` 后不会自愈，而且它**没有 `/sessions` 端点**可以清理（`GET /sessions` 返回 “HTTP method not allowed”）。`wd.py` 里已有 `ensure_driver()` 会检测 `ready:false` 并杀掉重启，不要再去写 `clear_sessions` 那种无效代码。

---

## 5. 环境

| 项 | 值 |
|---|---|
| 仓库 | `~/Workspace/agentd`（分支 `main`，远端 `origin`） |
| 服务 | `agentd.service`（`~/.local/bin/agentd serve`，0.0.0.0:18400） |
| 数据库 | `~/.data/agentd/agentd.db`（WAL） |
| 日志 | `~/.data/agentd/agentd.log` |
| 前端构建 | `cd web && npm run build`（agentd 从 `web/dist` 读盘，改前端只需构建，不用重启服务） |
| Go 构建 | `export PATH=$PATH:/usr/local/go/bin && go build -o ~/.local/bin/agentd ./cmd/agentd`，然后 `systemctl --user restart agentd` |
| 测试基座 | `~/.local/bin/geckodriver --port 4444` + `scripts/ui-test/wd.py` |

---

## 6. 已完成的改动（两个 commit）

### `68f61bd` 前端视觉重构 J + 分页载入 + 消息状态 + 编号统一 seq

**前端**

- `theme.css` 追加 J 视觉层：纸色网格、卡片切角、橙色结构标记（章节号 / 用户左线 / 异常值）、右栏标尺+信息块、窄屏上栏
- 右侧标题（横排右对齐）+ 半透明标尺（随轮数，点击跳转，hover 换标题预览）
- 标题宽度按 `--colw` 自适应，列宽拖大不再压正文
- 阅读位置 / 跳转 / peek 全部改用 `msg.seq`，**删掉 `vidx` 那套并行编号**
- 窄屏：右栏变上栏（轮次按钮 + 标题 + 横向进度条）

**后端**

- `GET /api/conv/<id>?rounds=N[&before=seq][&around=轮]` 分页：按轮切片、超 400 条二分降级（10→5→2→1）、按轮原子不切断
- `messages` 加 `status` 列（`running/done/interrupted/failed`）+ 启动扫描 `running → failed`
- 流式预分配 seq：首个 delta 时 `InsertPending` 占位，1s 节流 `UpdateMessage`，工具轮 `DeleteMessage` 回退
- SSE 新增 `seq` 事件；`LiveBubble` 带 `data-seq`
- 中断改为 status 标记，不再往正文追加 `（中断：…）`；一个字都没有才补一条说明
- 消息加派生 `kind`（`sysnote/interrupted/failed`），前端不再用正则猜系统提示
- 删除会话时取消正在跑的那一轮

### `677254e` 黑盒健壮性测试 + Esc 级联退出 + 测试脚手架重启

- 新增 `robustness.py`（7 组黑盒场景）
- **修**：Esc 从来没生效过（全仓 grep 不到 `Escape` 处理）。现在逐级退出：编辑器 → 详情 → 设置面板 → 抽屉，复用 `useSettings.closeOne`（有未保存改动仍走保存确认）
- **修**：`wd.py` 清残留会话用的是 `DELETE /session`（无 id，非标准调用），已改为检测 `ready:false` 后重启 geckodriver

---

## 7. 还没做的（按优先级）

| 优先级 | 项 | 说明 |
|---|---|---|
| P1 | `theme.css` 合并验证 | 工作区里待验证的那份 |
| P1 | `docs/AUDIT.md` | 分级问题清单，还没有文件 |
| P1 | `App.jsx` 的 `useEffect` 无依赖数组 | Esc 那个 effect 每次渲染重绑 listener，正确但浪费；评估是否改成 ref |
| P2 | 系统默认样式的 `<select>` | 设置面板里的 profile select 可能还是浏览器默认样式 |
| P2 | 点击目标过小 | 标尺已扩到 16px 高；其它小按钮（如 `.tn-tag`、`.dots`）可量一次 |
| P3 | 结构化 `error` 字段 | 中断原因目前只有文本 |
| P3 | 启动扫描的并行实例保护 | 现在直接全扫 `running`，单实例安全；多实例要加时间判断 |

---

## 8. 两个必须记住的教训

**① 不要手写时间标签。**

我曾在思考里连续写「时间：18:30 / 19:30」这类递增标签，实际只过了几分钟，于是产生“已经很晚了”的焦虑，开始跳验证 —— 而跳验证直接导致 `absCur` 多算偏移、`pageBase` 混用、工具轮残留 `running` 这几个 bug。

需要时间就跑 `date`，并且只在引用它的原始输出时提时间。

**② 不要用填充词占位。**

「写。」「快。」这种重复单字不代表任何进展，是思考退化的信号。思考只写三样：**要做什么、为什么、怎么验证**。卡住就直接说卡住。

---

## 9. 回滚点

```bash
git log --oneline -5
# 677254e  安全批（黑盒测试 + Esc + 测试脚手架）
# 68f61bd  J 视觉 + 分页 + status + seq 统一
# 1715c85  改动前（交接文档）
```

单批回退：`git revert <sha>`。全量回退：`git reset --hard 1715c85`（会丢 J 视觉层与分页，慎用）。

数据库层面新增过 `messages.status`（`ALTER TABLE`，默认 `''`，旧数据按 `done` 处理），回退代码不会自动删列，但不影响旧代码运行。
