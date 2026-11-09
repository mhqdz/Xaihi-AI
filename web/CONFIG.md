# 前端配置（不经后端）

`web/public/ui.json` 是纯前端配置：构建时复制到 `dist/ui.json`，浏览器用 `fetch('/ui.json')` 读取。
改它不需要动后端，也不需要改 Go 代码；改完刷新页面即可（生产环境重新 `npm run build` 或直接改 `web/dist/ui.json` 后刷新）。

```json
{
  "quips": ["运行中状态栏轮播的句子"],
  "appearance": {}
}
```

约定：

- 只放"展示层"的配置（文案、轮播、后续的外观/字号/间距开关）
- 不进 `~/.data/xaihi/*.json`，也不做成 `/api/*` 接口
- 代码里保留内置兜底：文件缺失或字段为空时仍能运行
