# xaihi 计划与待办

## 一、工具/插件体系的决策记录

2026-10-25：不引入插件（体量不值）→ 2026-10-28 推翻：kb/web 四个工具更适合脚本形态。

协议要点（详细实现见 §二、DESIGN.md §4）：

```
清单位置：~/.data/xaihi/tools.d/<name>.json（模板在 scripts/tools.d/，deploy 时 @REPO@ 替换）
一次性：command 直接 exec，参数走 AGENTD_ARG_*；超时 timeoutMs；非 0 退出 → 文本 + [exit N]
常驻：persistent:true → JSON-RPC over stdio（initialize / tools/list / tools/call）
作用域：仍由 profiles.json.tools 决定（不引入第二套开关）
不采用：Go plugin(.so)；在配置里表达分支逻辑
```

## 二、插件体系：已完成 / 待做

已完成（2026-10-28/29）：
- 清单目录 `~/.data/xaihi/tools.d/*.json`，模板入仓 `scripts/tools.d/`，deploy.sh 用 `@REPO@` 替换后同步（变更先备份）
- 一次性插件：直接 exec + `AGENTD_ARG_*` 环境变量 + stdin JSON；必填参数宿主侧校验
- 常驻插件：`persistent:true` → JSON-RPC over stdio（initialize/tools/list/tools/call），一进程多工具，
  自动重启、退出时 ShutdownPlugins()；示例 `scripts/tools/demo_persist.py`
- 前后端管理：`/api/plugins` 保存/删除清单；工具卡表单（名称/说明/命令/超时/常驻/参数/必填）；
  列表内置在前、外置在后，仅内置显示徽标；说明修改对插件写清单、对内置写 tools.json
- 文档：API.md「工具」与「插件的增删」、DESIGN.md §4

待做：
- MCP 客户端：直接复用常驻通道接标准 MCP server（配置里只写 command）
- 插件健康检查：`/api/tools` 返回 last_error / 上次耗时；连续失败给提示
- 插件批量导入：从目录扫描脚本生成清单；参数 schema 可从 `--describe` 推导
- 插件级权限：是否允许网络/写盘（现在没有沙箱）

## 三、交接/唤醒（HANDOFF.md）

M1 进行中：配方 → spawn_task → 子会话 → 执行记录 → outbox → /api/wake → 任务视图。
明确不做：资源探测/租约/排队互斥/命令白名单/外部编排插件；重启即 interrupted，人工收尾。

## 四、其他待办
- 工具说明再压缩（`recall` 649 字符 → ~200）以省上下文。
- 归档页「恢复」图标已做；删除会话 UI 尚未提供。
- 设置里显示"上次自动归档时间/数量"。
- 消息重试、会话重命名。

## 五、已完成但仍需沿用（部署脚本）

`scripts/deploy.sh` 已实现并验证（2026-10-26），部署时一律用它，不再手敲命令：

```
scripts/deploy.sh              # 构建后端 + 前端 → 重启 → 健康检查
scripts/deploy.sh --no-front   # 只发后端（前端没改时更快）
```

它的行为（改脚本时保持这四条）：
1. `go build -o /tmp/xaihi.new.$$ ./cmd/xaihi`：先构建到临时文件，构建失败不影响线上。
2. 未加 `--no-front` 时 `cd web && npm run build`（产物进 `web/dist`，18400 直接读它）。
3. `systemctl --user stop xaihi` → `cp` 到 `~/.local/bin/xaihi` → `chmod +x` → `start`。
4. `curl -s --noproxy '*' http://127.0.0.1:18400/api/health` 必须 200，否则打印
   `systemctl --user status --no-pager -n 15` 并 `exit 1`。

下一步打算（未做）：
- `--only-go` / `--only-web` 更细的粒度，避免无谓的整轮重启。
- 部署前自动跑 `go test ./...`，失败即中止。
- 部署成功后把 commit 短 id + 耗时写一行到 `~/.data/xaihi/xaihi.log`（用 `logx` 同款格式）。
