# agentd 审计报告

分级依据：P0 崩溃/报错/数据不一致 → P1 卡顿/状态错乱/响应式破坏/同角色样式不统一 → P2 与风格不合时宜/点击目标过小。

每条格式：**现象 → 根因 → 修复 → 验证**。未修的条目留在「待办」区，不预先标记结论。

## 审计环境

| 项 | 值 |
|---|---|
| 后端 | `127.0.0.1:18400`（systemd user unit `agentd`） |
| 数据库 | `~/.data/agentd/agentd.db`（WAL） |
| 浏览器 | geckodriver 4444 + Firefox headless |
| 视觉验证 | `scripts/ui-test/pixel_diff.py`，6 张固定页面/尺寸 |
| 基线 | pixel_diff 首次输出的 6 张 PNG |

审计起点的自检：对**未改动的 HEAD 构建**跑一次 `pixel_diff compare`，6/6 逐像素一致，证明截图链路确定性足够、基线有效。此后任何视觉提交都必须保持「除刻意改动的那几张外，其余逐像素不变」。

---

## P0 — 崩溃 / 报错 / 数据不一致

暂无已复现条目。

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

---

## P2 — 与当前风格不合时宜 / 点击目标过小

### P2-1 表单下拉框仍是浏览器默认箭头 ⬜ 待修

**现象**：设置 → 模型详情的「思考强度」「服务商」两个下拉框，右侧是浏览器给出的细箭头；而输入框上方「PROFILE」下拉框用的是自定义 `▾`（深绿灰、更粗）。同一产品里并存两种下拉箭头。

**根因（已核实，非目测）**：`.composer .pick select`（`theme.css:119`）设了 `appearance:none` 并额外渲染 `<span className="caret">▾</span>`；`.card .field select`（`theme.css:303`）只给了边框和底色，**没有重置 `appearance`**，箭头因此来自 UA 而不是样式表。

证据：打开模型详情后取计算样式 —— `appearance: "auto"`、`backgroundImage: "none"`、`backgroundColor: rgb(255,255,255)`。截图见 `model_before.png`（1600×1000）。

**修复**：未开始。

**验证**：待补。

### P2-2 数字输入框的原生 spinner ⬜ 待修

**现象**：模型详情的「上下文窗口 / 输入上限 / 压缩阈值」、设置一级卡的「轮次上限 / 未活跃天数 / 保留天数」，右端都有 UA 的上下小箭头（spinner）。

**根因**：`<input type="number">` 未处理 `appearance` 与 `::-webkit-inner-spin-button`。已核实这些字段确实是 `type="number"`（`ModelForm.jsx:40/44`、`ListCard.jsx`），DOM 探测到 3 个 number input 均宽 359px 且带 spinner。

**修复**：未开始。注意这些字段里只有「轮次上限 50 / 未活跃天数 30 / 保留天数 7」是 ±1 有意义的小整数，其余是五位数以上，spinner 的 8px 点击区本身也偏小。

**验证**：待补。

---

## 待核实（尚无证据，不下结论）

| 编号 | 观察 | 需要的证据 |
|---|---|---|
| Q-1 | 900px 宽下顶栏左端被抽屉压住（`narrow-conv51` 顶部） | 取 header 与 drawer 的 getBoundingClientRect 判断是否重叠 |
| Q-2 | 空状态页右栏显示 `01 / 1`，会话页显示 `23 / 23`，位置一上一下 | 查 rail 布局是否随内容高度漂移 |
| Q-3 | 右栏底部同时出现 `−`、`-`、`+` 三种符号 | 放大截图 + 查对应 DOM 节点 |
| Q-4 | 抽屉底部在 `WORKSPACE / AGENT` 下方留约 35px 空白条 | 量 drawer 与内层列表高度 |

---

## 批次记录

| 批次 | 内容 | commit | 视觉验证 |
|---|---|---|---|
| 1 | P1-1 + P1-2 空状态按钮并入 `.btn` 体系并等宽 | 待填 | 4/6 逐像素一致，2/6 差异限于按钮列 |
