// Package tools：agent 能用的工具。返回 text，或 text+image 的内容数组（由 web 层决定怎么展示）。
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"unicode/utf8"
	"xaihi/internal/config"
	"xaihi/internal/logx"

	"time"
	"xaihi/internal/hook"
	"xaihi/internal/journal"
	"xaihi/internal/memory"
	"xaihi/internal/store"
	"xaihi/internal/todo"
	"xaihi/internal/toolmeta"
)

// shotQuota 由 main 注入：看屏幕的每日配额（返回"还有没有额度"和"今天已用几张"）。
// 为 nil 时不限（测试/独立运行）。
var shotQuota func() (bool, int)

// SetShotQuota 装配截图配额（进程级，启动时调一次）
func SetShotQuota(f func() (bool, int)) { shotQuota = f }

// Env：工具需要的会话上下文（recall 用）。Messages 传 []store.Message，避免包循环。
type Env struct {
	Messages any
	// SpawnTask：派发一个已注册任务（父会话用），返回任务 id
	SpawnTask func(kind string, card map[string]any) (int64, error)
	// FailTask：子会话汇报失败事实（父会话不给这个工具）
	FailTask func(reason, evidence string) (string, error)
	// Memory：长期记忆（跨会话）；为 nil 时记忆工具不可用
	Memory *memory.Store
	// ConvID：当前会话（钩子绑会话用：在哪个会话里说"盯着X"，就在那个会话里提醒）
	ConvID int64
}

type Spec struct {
	Name        string
	Description string
	Source      string   // builtin（Go 内置）或 plugin（外置插件）
	Path        string   // 插件清单路径（builtin 为空）
	Command     []string // 插件启动命令（builtin 为空；配置界面要回显它才能编辑）
	TimeoutMs   int      // 插件声明的超时（0 = 内置/默认）
	Persistent  bool     // 是否常驻进程
	// Category：工具分类（core / memory / hook / image），配置界面按它分组，
	// 也用它决定"这个工具要不要先开某个 profile 开关"。
	Category string
	// Needs：需要的模型能力（目前只有 vision = 能接收图像）。
	// profile 的 input 不含 image 时，这类工具不该被选上。
	Needs    []string
	Params   map[string]any // JSON Schema properties
	Required []string
	Run      func(ctx context.Context, args map[string]any, env *Env) (any, error)
}

// 最近一次 Registry() 里插件加载失败的说明。
// 以前这些只有 logx.Warn 一行日志：你在界面上给插件打开了「常驻进程」，
// 保存成功，然后它从工具列表里消失了 —— 没有任何提示。
var lastWarns atomic.Value

// LoadWarnings 返回最近一次加载时被跳过的插件及原因。
func LoadWarnings() []string {
	if v := lastWarns.Load(); v != nil {
		if ws, ok := v.([]string); ok && ws != nil {
			return ws
		}
	}
	// 返回空切片而不是 nil：JSON 里是 [] 而不是 null，调用方不用做两种判断
	return []string{}
}

func Registry() map[string]*Spec {
	m := map[string]*Spec{}
	defer func() {
		// 没标分类的一律 core（插件默认也是 core）
		for _, sp := range m {
			if sp.Category == "" {
				sp.Category = toolmeta.GroupChat
			}
		}
	}()
	for _, s := range []*Spec{
		{Name: "bash", Description: "在本机执行一条 shell 命令（默认 180 秒超时，到点杀整个进程组）。",
			Params: map[string]any{"command": map[string]any{"type": "string", "description": "完整 shell 命令"},
				"timeout": map[string]any{"type": "integer", "description": "超时秒数，默认 180，最大 600"}},
			Required: []string{"command"}, Run: runBash},
		{Name: "screenshot", Category: toolmeta.GroupView, Needs: []string{"vision"}, Description: "截屏并返回文字说明 + 截屏原图（模型能看到像素）。用于判断窗口内容/排版/报错。",
			Params: map[string]any{"question": map[string]any{"type": "string", "description": "可留空；留空只返回截图，填了也不会回显"}},
			Run:    runScreenshot},
		{Name: "view_image", Category: toolmeta.GroupView, Needs: []string{"vision"}, Description: "打开本机的一张图片给模型看（png/jpg/webp/gif，≤8MB）。" +
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
		{Name: "memory_save", Category: toolmeta.GroupPersist, Description: "记住一件事（写进长期记忆，下次对话会出现在系统提示里）。" +
			"只记「需要一直在场」的东西：偏好、关系、约定、关键经历。事实/资料类不要写这里，写 kb。" +
			"一条只写一件事，一句话能说清；写多了会挤掉别的记忆。",
			Params: map[string]any{
				"body": map[string]any{"type": "string", "description": "正文：一句话，不加标题"},
				"kind": map[string]any{"type": "string", "description": "偏好 / 约定 / 经历 / 事实（默认记录）"},
			}, Required: []string{"body"}, Run: runMemorySave},
		{Name: "memory_forget", Category: toolmeta.GroupPersist, Description: "忘掉一条记忆（软删除：标 archived，不进提示词但文件保留、可恢复）。" +
			"name 用 memory_list 返回的名字。",
			Params: map[string]any{
				"name": map[string]any{"type": "string", "description": "记忆名字（memory_list 里的 name）"},
			}, Required: []string{"name"}, Run: runMemoryForget},
		{Name: "hook_add", Category: toolmeta.GroupPersist, Description: "给她设一个「闹钟」：以后由外部事件（或定时）触发时，她会在某个会话里被叫醒。" +
			"参数 name（小写字母数字连字符）、prompt（叫醒时看到的话）、mode（speak 说话 / silent 只做事）、" +
			"quota（每天最多几次，默认 10）、cooldown（秒，默认 3）。不会立刻执行，只是注册。",
			Params: map[string]any{"name": map[string]any{"type": "string"}, "prompt": map[string]any{"type": "string"},
				"mode": map[string]any{"type": "string"}, "quota": map[string]any{"type": "integer"},
				"cooldown": map[string]any{"type": "integer"}}, Required: []string{"name", "prompt"},
			Run: runHookAdd},
		{Name: "hook_list", Category: toolmeta.GroupPersist,
			Description: "列出已注册的钩子（名字/模式/配额/冷却/属于哪个会话）", Run: runHookList},
		{Name: "hook_rm", Category: toolmeta.GroupPersist, Description: "删掉一个钩子",
			Params: map[string]any{"name": map[string]any{"type": "string"}}, Required: []string{"name"},
			Run: runHookRemove},
		{Name: "journal_write", Category: toolmeta.GroupPersist, Description: "写今天的日记（第一人称，记「今天发生了什么、我怎么想」）。" +
			"一天一个文件、最多 500 字；事实性的东西用 memory_save，不要混进来。",
			Params: map[string]any{"text": map[string]any{"type": "string"}}, Required: []string{"text"},
			Run: runJournalWrite},
		{Name: "todo_add", Category: toolmeta.GroupTodo, Description: "记一条待办（以后该提醒他的事）。" +
			"what 是要提醒的内容，at 是到期时间（RFC3339 或 2006-01-02 15:04，留空 = 不指定时间）。" +
			"它不会自己响 —— 到点提醒由外部脚本读 todos.json 触发。",
			Params:   map[string]any{"what": map[string]any{"type": "string"}, "at": map[string]any{"type": "string"}},
			Required: []string{"what"}, Run: runTodoAdd},
		{Name: "todo_list", Category: toolmeta.GroupTodo, Description: "列出待办（未完成在前，含到期时间与完成状态）",
			Run: runTodoList},
		{Name: "todo_done", Category: toolmeta.GroupTodo, Description: "把一条待办标记为完成",
			Params: map[string]any{"id": map[string]any{"type": "string"}}, Required: []string{"id"},
			Run: runTodoDone},
		{Name: "memory_list", Category: toolmeta.GroupPersist, Description: "列出记得的事（全局 + 当前项目的 active 记忆），含名字、类型、时间、正文。" +
			"回答「你记得什么」或准备忘掉某条之前用。",
			Params: map[string]any{}, Run: runMemoryList},
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
	// 外置插件：~/.data/xaihi/tools.d/*.json（同名以内置为准）
	{
		plugs, warns := LoadPlugins(config.DataPath("tools.d"))
		// 无条件存：否则删掉坏插件后，上一次的警告会一直挂在界面上
		lastWarns.Store(warns)
		if len(plugs) > 0 || len(warns) > 0 {
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

// intArg 读一个整型参数（JSON 解出来是 float64），缺省或非法时用 def
func intArg(args map[string]any, k string, def int) int {
	switch v := args[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
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
	if shotQuota != nil {
		if ok, used := shotQuota(); !ok {
			return fmt.Sprintf("✗ 今天看屏幕的配额用完了（300 张，已用 %d）。明天再来，或者让用户直接描述。", used), nil
		}
	}
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
	// 压缩：glyph 级别的截图（1080p PNG）实测 2.8MB，转 JPEG 后 200KB 左右。
	// 原样塞给模型时一次请求能到 7.7MB（两张图），撞哪一层都可能断。
	// ffmpeg 不在就退回 PNG —— 宁可大也不能没有截图。
	out := name
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		jpg := strings.TrimSuffix(img, ".png") + ".jpg"
		c := exec.CommandContext(ctx, "ffmpeg", "-y", "-loglevel", "error",
			"-i", img, "-vf", "scale='min(1920,iw)':-2", "-q:v", "4", jpg)
		if err := c.Run(); err == nil {
			_ = os.Remove(img)
			out = filepath.Base(jpg)
		}
	}
	// **不自动把图塞进上下文**：只给路径。
	// 图是证据，不是对话内容 —— 自动带的后果是它跟着之后每一次请求重发
	// （实测 7.7MB 的请求体）。要看就调 view_image，那一次请求才付这个成本。
	return "截屏完成：/shots/" + out + "\n（要看内容就调 view_image；不读的话它不会占上下文）", nil
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
	// 截图返回的是 "/shots/x.jpg" 这种 URL，读图时转成数据目录里的真实路径
	if strings.HasPrefix(p, "/shots/") {
		p = config.DataPath(strings.TrimPrefix(p, "/"))
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

// ---------- 长期记忆 ----------

func runMemorySave(ctx context.Context, args map[string]any, env *Env) (any, error) {
	if env.Memory == nil {
		return "✗ 记忆不可用（没有记忆根目录）", nil
	}
	body, _ := args["body"].(string)
	kind, _ := args["kind"].(string)
	if strings.TrimSpace(kind) == "" {
		kind = "记录"
	}
	e, err := env.Memory.Save("", kind, "", body)
	if err != nil {
		return "✗ " + err.Error(), nil
	}
	return fmt.Sprintf("已记住（%s，%s）", e.Name, e.Created()), nil
}

func runMemoryForget(ctx context.Context, args map[string]any, env *Env) (any, error) {
	if env.Memory == nil {
		return "✗ 记忆不可用", nil
	}
	name, _ := args["name"].(string)
	if err := env.Memory.Forget(name); err != nil {
		return "✗ " + err.Error(), nil
	}
	return "已忘掉 " + name + "（文件还留着，改回 active 就能恢复）", nil
}

func runMemoryList(ctx context.Context, args map[string]any, env *Env) (any, error) {
	if env.Memory == nil {
		return "✗ 记忆不可用", nil
	}
	es, err := env.Memory.List()
	if err != nil {
		return "✗ " + err.Error(), nil
	}
	if len(es) == 0 {
		return "（还没有记忆）", nil
	}
	var b strings.Builder
	for _, e := range es {
		kind := e.Field("kind")
		if kind == "" {
			kind = "记录"
		}
		fmt.Fprintf(&b, "%s  [%s] %s\n    %s\n", e.Name, kind, e.Created(), e.Body)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ---------- 钩子（触发器）----------
//
// 和 CLI 走同一套文件（hooks/*.json），只是入口不同：这里让 agent 在对话里
// 把"盯着 steam，玩久了提醒我"直接变成一条钩子，不用人去跑命令。
// 文件仍由 xaihi 维护（Write 负责校验与原子写），热重载 5 秒内生效。

func hookDir() string { return config.DataPath("hooks") }

func runHookAdd(_ context.Context, args map[string]any, env *Env) (any, error) {
	name := strings.TrimSpace(str(args, "name"))
	prompt := strings.TrimSpace(str(args, "prompt"))
	mode := strings.TrimSpace(str(args, "mode"))
	if mode == "" {
		mode = hook.ModeSpeak
	}
	d := hook.Def{
		Name: name, Prompt: prompt, Mode: mode,
		QuotaPerDay: intArg(args, "quota", 0),
		CooldownS:   intArg(args, "cooldown", 0),
		ConvID:      env.ConvID, // 绑到当前会话：在哪儿交代的，就在哪儿回话
	}
	if err := hook.Write(hookDir(), d); err != nil {
		return "✗ " + err.Error(), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "已注册钩子 %s（%s，配额 %d/天，冷却 %ds）", d.Name, d.Mode,
		d.QuotaOrDefault(), d.CooldownOrDefault())
	if d.ConvID > 0 {
		b.WriteString(fmt.Sprintf("，触发时写回会话 #%d", d.ConvID))
	}
	b.WriteString("\n它不会立刻执行 —— 由外部事件触发（cron / 脚本 / 观察器）。5 秒内生效。")
	return b.String(), nil
}

func runHookList(_ context.Context, _ map[string]any, _ *Env) (any, error) {
	defs, _ := hook.LoadDir(hookDir())
	if len(defs) == 0 {
		return "（还没有钩子）", nil
	}
	var b strings.Builder
	for _, d := range defs {
		fmt.Fprintf(&b, "- %s  %s  配额 %d/天  冷却 %ds  会话 %s\n    %s\n",
			d.Name, d.Mode, d.QuotaOrDefault(), d.CooldownOrDefault(),
			convText(d.ConvID), oneLineTxt(d.Prompt))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func runHookRemove(_ context.Context, args map[string]any, _ *Env) (any, error) {
	name := strings.TrimSpace(str(args, "name"))
	if err := hook.Remove(hookDir(), name); err != nil {
		return "✗ " + err.Error(), nil
	}
	return "已删除钩子 " + name + "（5 秒内生效）", nil
}

func convText(id int64) string {
	if id <= 0 {
		return "收件箱"
	}
	return fmt.Sprintf("#%d", id)
}

func oneLineTxt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

// ---------- 待办 ----------

func todoStore() *todo.Store { return todo.Open(config.DataPath("todos.json")) }

// parseTodoAt 接受两种写法：RFC3339（带时区）和 "2006-01-02 15:04"（按本机时区）
func parseTodoAt(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02", "15:04"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("时间看不懂：%q（用 2026-11-11T09:00:00+08:00 或 2026-11-11 09:00）", v)
}

func runTodoAdd(_ context.Context, args map[string]any, env *Env) (any, error) {
	what := strings.TrimSpace(str(args, "what"))
	at, err := parseTodoAt(str(args, "at"))
	if err != nil {
		return "✗ " + err.Error(), nil
	}
	it, err := todoStore().Add(what, at, env.ConvID)
	if err != nil {
		return "✗ " + err.Error(), nil
	}
	when := "不指定时间"
	if !at.IsZero() {
		when = at.Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("已记下待办 %s：%s（%s）", it.ID, it.What, when), nil
}

func runTodoList(_ context.Context, _ map[string]any, _ *Env) (any, error) {
	items, err := todoStore().List()
	if err != nil {
		return "✗ " + err.Error(), nil
	}
	if len(items) == 0 {
		return "（没有待办）", nil
	}
	var b strings.Builder
	for _, it := range items {
		mark := "○"
		if it.Done {
			mark = "●"
		}
		fmt.Fprintf(&b, "%s %s  %s", mark, it.ID, it.What)
		if !it.At.IsZero() {
			fmt.Fprintf(&b, "（%s）", it.At.Format("01-02 15:04"))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func runTodoDone(_ context.Context, args map[string]any, _ *Env) (any, error) {
	id := strings.TrimSpace(str(args, "id"))
	if err := todoStore().Done(id); err != nil {
		return "✗ " + err.Error(), nil
	}
	return "已标记完成：" + id, nil
}

// ---------- 日记 ----------

func journalStore() *journal.Store { return &journal.Store{Dir: config.DataPath("journal")} }

func runJournalWrite(_ context.Context, args map[string]any, _ *Env) (any, error) {
	text := strings.TrimSpace(str(args, "text"))
	now := time.Now()
	if err := journalStore().Append(now, now, text); err != nil {
		return "✗ " + err.Error(), nil
	}
	day := now.Format("2006-01-02")
	return fmt.Sprintf("已写进今天的日记（%s）。想回顾就读 %s.md。", day, day), nil
}
