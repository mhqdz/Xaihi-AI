// Package tools：agent 能用的工具。返回 text，或 text+image 的内容数组（由 web 层决定怎么展示）。
package tools

import (
	"agentd/internal/config"
	"agentd/internal/logx"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf8"

	"agentd/internal/store"
	"time"
)

// Env：工具需要的会话上下文（recall 用）。Messages 传 []store.Message，避免包循环。
type Env struct {
	Messages any
	// SpawnTask：派发一个已注册任务（父会话用），返回任务 id
	SpawnTask func(kind string, card map[string]any) (int64, error)
	// FailTask：子会话汇报失败事实（父会话不给这个工具）
	FailTask func(reason, evidence string) (string, error)
}

type Spec struct {
	Name        string
	Description string
	Source      string         // builtin（Go 内置）或 plugin（外置插件）
	Path        string         // 插件清单路径（builtin 为空）
	TimeoutMs   int            // 插件声明的超时（0 = 内置/默认）
	Persistent  bool           // 是否常驻进程
	Params      map[string]any // JSON Schema properties
	Required    []string
	Run         func(ctx context.Context, args map[string]any, env *Env) (any, error)
}

func Registry() map[string]*Spec {
	m := map[string]*Spec{}
	for _, s := range []*Spec{
		{Name: "bash", Description: "在本机执行一条 shell 命令（默认 180 秒超时，到点杀整个进程组）。",
			Params: map[string]any{"command": map[string]any{"type": "string", "description": "完整 shell 命令"},
				"timeout": map[string]any{"type": "integer", "description": "超时秒数，默认 180，最大 600"}},
			Required: []string{"command"}, Run: runBash},
		{Name: "screenshot", Description: "截屏并返回文字说明 + 截屏原图（模型能看到像素）。用于判断窗口内容/排版/报错。",
			Params: map[string]any{"question": map[string]any{"type": "string", "description": "可留空；留空只返回截图，填了也不会回显"}},
			Run:    runScreenshot},
		{Name: "view_image", Description: "打开本机的一张图片给模型看（png/jpg/webp/gif，≤8MB）。" +
			"用于验收渲染产物、查看截图文件；支持 ~/ 开头和通配符（取第一个匹配）。",
			Params: map[string]any{
				"path": map[string]any{"type": "string", "description": "图片路径（绝对路径或 ~/…）"},
			}, Required: []string{"path"}, Run: runViewImage},
		{Name: "spawn_task", Description: "派发一个已注册的后台任务（会换模型/让出显卡，完成后回到本会话回报）。" +
			"kind 必须是任务注册表里的名字；card 是交接卡内容（目标/输入/约束/产物）。",
			Params: map[string]any{
				"kind": map[string]any{"type": "string", "description": "任务类型（见 tasks.d 注册表）"},
				"card": map[string]any{"type": "object", "description": "交接卡：目标、输入、约束、期望产物"},
			}, Required: []string{"kind", "card"}, Run: runSpawnTask},
		{Name: "handoff_fail", Description: "子会话无法继续时调用：只写事实（命令、退出码、关键行、产物路径），不要解释原因。",
			Params: map[string]any{
				"reason":   map[string]any{"type": "string", "description": "一句话事实，如 CUDA OOM"},
				"evidence": map[string]any{"type": "string", "description": "证据：命令、退出码、日志路径"},
			}, Required: []string{"reason"}, Run: runHandoffFail},
		{Name: "recall", Description: "在**本会话**历史里检索（不跨会话）。默认在消息正文里按关键词 OR 匹配，返回命中的片段与序号。" +
			"需要正则时加 re=true（Go RE2 语法，示例: (鹈鹕|pelican).*(车|bike)）。" +
			"想看某条附近上下文用 around=<序号>。level=raw 才返回整段原文（贵，慎用）。" +
			"软限制：一次最多 3 条；不够就先问用户，不要反复 recall。",
			Params: map[string]any{
				"query":  map[string]any{"type": "string", "description": "关键词（空格分词，默认 OR）或正则"},
				"re":     map[string]any{"type": "boolean", "description": "query 按正则解释"},
				"all":    map[string]any{"type": "boolean", "description": "关键词全部命中（AND）"},
				"not":    map[string]any{"type": "string", "description": "排除词"},
				"around": map[string]any{"type": "integer", "description": "序号；返回它附近 ±2 条"},
				"level":  map[string]any{"type": "string", "description": "summary(默认) 或 raw"},
				"limit":  map[string]any{"type": "integer", "description": "最多几条，默认 3"},
			}, Run: runRecall},
	} {
		s.Source = "builtin"
		m[s.Name] = s
	}
	// 外置插件：~/.data/agentd/tools.d/*.json（同名以内置为准）
	{
		if plugs, warns := LoadPlugins(config.DataPath("tools.d")); len(plugs) > 0 || len(warns) > 0 {
			for name, sp := range plugs {
				if _, exists := m[name]; exists {
					continue
				}
				m[name] = sp
			}
			for _, w := range warns {
				logx.Warn("plugin.load %s", w)
			}
		}
	}
	applyOverrides(m)
	return m
}

func SchemaJSON(reg map[string]*Spec) []map[string]any {
	out := make([]map[string]any, 0, len(reg))
	for _, s := range reg {
		// OpenAI 严格校验：required 必须是数组（不能是 null），properties 必须是对象
		req := s.Required
		if req == nil {
			req = []string{}
		}
		props := s.Params
		if props == nil {
			props = map[string]any{}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": s.Name, "description": s.Description,
				"parameters": map[string]any{"type": "object", "properties": props, "required": req},
			},
		})
	}
	return out
}

func str(args map[string]any, k string) string {
	if v, ok := args[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func runBash(ctx context.Context, args map[string]any, _ *Env) (any, error) {
	cmd := str(args, "command")
	if strings.TrimSpace(cmd) == "" {
		return "[error] 缺少 command", nil
	}
	t := 180
	if v, ok := args["timeout"].(float64); ok && v > 0 {
		t = int(v)
	}
	if t < 5 {
		t = 5
	}
	if t > 600 {
		t = 600
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(t)*time.Second)
	defer cancel()
	c := exec.CommandContext(cctx, "/bin/bash", "-lc", cmd)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Dir, _ = os.UserHomeDir()
	var out strings.Builder
	c.Stdout, c.Stderr = &out, &out
	// 先 Start（c.Process 由此确定），再用看门狗在超时时杀整个进程组，
	// 最后 Wait。必须先 Start 再起 goroutine，否则 c.Process 会和 Run 内部写入竞争。
	if err := c.Start(); err != nil {
		return "[error] " + err.Error(), nil
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-cctx.Done():
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		case <-done:
		}
	}()
	err := c.Wait()
	close(done)
	if cctx.Err() == context.DeadlineExceeded {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return fmt.Sprintf("[timeout] 命令超过 %d 秒，已杀掉进程组\n%s", t, clipOutput(out.String(), 4000)), nil
	}
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return "[error] " + err.Error(), nil
		}
	}
	return fmt.Sprintf("%s\n[exit %d]", clipOutput(out.String(), 8000), code), nil
}

func runScreenshot(ctx context.Context, args map[string]any, _ *Env) (any, error) {
	_ = strings.TrimSpace(str(args, "question")) // 参数保留但不再回显（问题已在用户消息里）
	dir := config.DataPath("shots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "✗ 建截图目录失败：" + err.Error(), nil
	}
	name := fmt.Sprintf("%d.png", time.Now().UnixNano())
	img := filepath.Join(dir, name)
	env := append(os.Environ(), "XDG_RUNTIME_DIR=/run/user/"+fmt.Sprint(os.Getuid()))
	// 只在环境里没有时才补默认。硬编码 "WAYLAND_DISPLAY=wayland-1" 会覆盖继承值，
	// 用户切到 wayland-0 或换机器时截图直接失败（而且错误信息只显示 grim 报的 socket 路径）。
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		env = append(env, "WAYLAND_DISPLAY=wayland-1")
	}
	grim := exec.CommandContext(ctx, "grim", img)
	grim.Env = env
	if err := grim.Run(); err != nil {
		return "✗ 截图失败：" + err.Error(), nil
	}
	// 消息里只存 URL（前端/模型侧都用它）；读盘转 data URL 由 toWire 负责 ✓
	return []map[string]any{
		{"type": "text", "text": "截屏完成：当前屏幕截图如下。"},
		{"type": "image", "url": "/shots/" + name},
	}, nil
}

// clipOutput：超长时保留首尾并标明"省略了多少、完整内容在哪"，绝不静默丢弃
func clipOutput(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit || limit <= 0 {
		return s
	}
	dir := config.DataPath("out")
	_ = os.MkdirAll(dir, 0o755)
	name := fmt.Sprintf("%d.txt", time.Now().UnixNano())
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
		return string(r[:limit]) + fmt.Sprintf("\n…（还有 %d 字符未显示）", len(r)-limit)
	}
	head := limit * 6 / 10
	tailN := limit - head
	return string(r[:head]) +
		fmt.Sprintf("\n…（中间省略 %d 字符）\n完整输出已存 %s（共 %d 字符），需要看某段用 sed -n '起,止p' %s\n…\n",
			len(r)-limit, full, len(r), full) +
		string(r[len(r)-tailN:])
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…（前略）\n" + string(r[len(r)-n:])
}

var _ = json.Marshal

// runRecall：只搜本会话。RE2 线性时间（无回溯爆炸），外加 300ms 兜底超时。
func runRecall(ctx context.Context, args map[string]any, env *Env) (any, error) {
	if env == nil || env.Messages == nil {
		return "✗ 没有会话上下文可检索", nil
	}
	msgs, ok := env.Messages.([]store.Message)
	if !ok {
		return "✗ 会话上下文类型不对", nil
	}
	level := str(args, "level")
	if level == "" {
		level = "summary"
	}
	limit := 3
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	// --around：直接给附近几条
	if v, ok := args["around"].(float64); ok && v > 0 {
		center := int(v)
		var out []string
		for _, m := range msgs {
			if m.Seq >= center-2 && m.Seq <= center+2 {
				out = append(out, fmt.Sprintf("#%d [%s] %s", m.Seq, m.Role, truncate(m.Content, 400)))
			}
		}
		if len(out) == 0 {
			return fmt.Sprintf("（序号 %d 附近没有消息）", center), nil
		}
		return strings.Join(out, "\n"), nil
	}

	q := str(args, "query")
	if strings.TrimSpace(q) == "" {
		return "✗ 缺少 query", nil
	}
	var match func(string) bool
	if b, _ := args["re"].(bool); b {
		re, err := regexp.Compile(q)
		if err != nil {
			return "✗ 正则编译失败（Go RE2 语法）：" + err.Error(), nil
		}
		match = re.MatchString
	} else {
		words := strings.Fields(q)
		all, _ := args["all"].(bool)
		not := str(args, "not")
		match = func(s string) bool {
			ls := strings.ToLower(s)
			hit := 0
			for _, w := range words {
				if strings.Contains(ls, strings.ToLower(w)) {
					hit++
				}
			}
			if not != "" && strings.Contains(ls, strings.ToLower(not)) {
				return false
			}
			if all {
				return hit == len(words)
			}
			return hit > 0
		}
	}

	type hit struct {
		seq    int
		role   string
		snip   string
		approx bool
	}
	var hits []hit
	for _, m := range msgs {
		select {
		case <-cctx.Done():
			return "✗ 检索超时（300ms），请把 query 缩小一点", nil
		default:
		}
		body := m.Content
		if level == "summary" && m.Role == "tool" {
			body = truncate(body, 400) // 工具输出只截前 400 字参与匹配，避免大块文本
		}
		if match(body) {
			hits = append(hits, hit{m.Seq, m.Role, snippet(body, q, 60), level == "summary"})
			if len(hits) >= limit {
				break
			}
		}
	}
	if len(hits) == 0 {
		return "（本会话没有命中）", nil
	}
	var out []string
	for _, h := range hits {
		out = append(out, fmt.Sprintf("#%d [%s] %s", h.seq, h.role, h.snip))
	}
	return strings.Join(out, "\n"), nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// snippet：关键词附近 ±60 字；正则可选则退化为前 120 字。
func snippet(s, q string, window int) string {
	rs := []rune(s)
	low := strings.ToLower(s)
	if w := strings.Fields(q); len(w) > 0 && !strings.ContainsAny(q, "()|*+?[{\\") {
		if idx := strings.Index(low, strings.ToLower(w[0])); idx >= 0 {
			// strings.Index 给的是**字节**偏移，而 rs 是 **rune** 切片。
			// 中文一个字 3 字节，直接拿字节偏移去切 rune：轻则错位，重则
			// idx+window 永远超出 len(rs)，整个函数退化成「返回前 120 字」——
			// 于是中文会话里的检索摘要永远不含命中的关键词。
			ri := utf8.RuneCountInString(s[:idx])
			if ri+window < len(rs) && ri-window > 0 {
				return "…" + string(rs[ri-window:ri+window]) + "…"
			}
		}
	}
	return truncate(s, 120)
}

func runSpawnTask(_ context.Context, args map[string]any, e *Env) (any, error) {
	if e == nil || e.SpawnTask == nil {
		return "[error] 当前会话不能派发任务", nil
	}
	kind, _ := args["kind"].(string)
	card, _ := args["card"].(map[string]any)
	if strings.TrimSpace(kind) == "" {
		return "[error] 缺少 kind", nil
	}
	id, err := e.SpawnTask(kind, card)
	if err != nil {
		return "[error] " + err.Error(), nil
	}
	return fmt.Sprintf("已派发 task #%d（%s），完成后会在本会话回报", id, kind), nil
}

func runHandoffFail(_ context.Context, args map[string]any, e *Env) (any, error) {
	reason, _ := args["reason"].(string)
	evidence, _ := args["evidence"].(string)
	if e == nil || e.FailTask == nil {
		return "[error] 只有子会话可以调用 handoff_fail", nil
	}
	msg, err := e.FailTask(reason, evidence)
	if err != nil {
		return "[error] " + err.Error(), nil
	}
	return msg, nil
}

// view_image：把本地图片读成 data URL 交给模型（与截图工具同样的 content parts 形态）
func runViewImage(_ context.Context, args map[string]any, _ *Env) (any, error) {
	p := strings.TrimSpace(str(args, "path"))
	if p == "" {
		return "[error] 缺少 path", nil
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if strings.ContainsAny(p, "*?[") {
		if ms, _ := filepath.Glob(p); len(ms) > 0 {
			p = ms[0]
		}
	}
	st, err := os.Stat(p)
	if err != nil {
		return "✗ 文件不存在：" + p, nil
	}
	if st.IsDir() {
		return "✗ 这是一个目录：" + p, nil
	}
	if st.Size() > 8<<20 {
		return fmt.Sprintf("✗ 图片太大（%.1f MB，上限 8 MB）：%s", float64(st.Size())/1048576, p), nil
	}
	mime := mime.TypeByExtension(strings.ToLower(filepath.Ext(p)))
	if !strings.HasPrefix(mime, "image/") {
		return "✗ 不支持的图片类型：" + filepath.Ext(p), nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "✗ 读文件失败：" + err.Error(), nil
	}
	return []map[string]any{
		{"type": "text", "text": fmt.Sprintf("图片：%s（%d KB）", p, st.Size()/1024)},
		{"type": "image", "url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)},
	}, nil
}
