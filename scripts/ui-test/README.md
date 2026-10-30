# UI 自动化测试（WebDriver）

不依赖 selenium：用 `geckodriver` 的 HTTP 接口直连 Firefox headless。
脚本会断言排版（溢出/宽度/层级显隐）与交互（思考展开收起、设置三级卡、任务展开、抽屉），并保存截图。

## 用法

```bash
# 1) 起 geckodriver（一次性）
/tmp/geckodriver --port 4444 &

# 2) 跑
python3 scripts/ui-test/run.py             # 主用例：宽屏/窄屏/设置三级卡/输入框/任务
python3 scripts/ui-test/run2.py            # 错误收集/任务点击/滚动/输入框间距
python3 scripts/ui-test/run3.py            # 任务深链滚动/竖屏布局
python3 scripts/ui-test/check_layout.py    # 设置面板层级布局一致性（2 级/3 级 模型=工具，宽窄屏几何）
python3 scripts/ui-test/check_settings.py  # 设置面板行为（脏检查/保存/取消/深链/localStorage）
python3 scripts/ui-test/check_sendstop.py  # 发送/中止的按钮过场（loading→stop→普通，防重复提交）
python3 scripts/ui-test/check_tools.py     # 工具组结构与 memo（对话页，不涉及设置）
python3 scripts/ui-test/check_live.py      # 真跑一轮对话（实时气泡收尾、历史卡不重渲染）
                                           #   会新建一条会话，跑完自动归档

# 截图落在 /tmp/uitest/*.png
```

`wd.py` 是最小 WebDriver 客户端（创建会话、导航、点击、执行 JS、截图）。
Firefox 窗口大于屏幕时用 `POST /session/{id}/window/rect` 显式设置尺寸。
