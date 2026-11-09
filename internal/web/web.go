// Package web：P1 的 HTTP + SSE 层。前端只跟这里的接口说话。
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"agentd/internal/llm"
	"agentd/internal/logx"

	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"agentd/internal/agent"
	"agentd/internal/config"
	"agentd/internal/handoff"
	"agentd/internal/hook"
	"agentd/internal/store"
	"agentd/internal/tools"
)

type Server struct {
	jobMu       sync.Mutex
	jobs        map[string]*job
	convMu      sync.Mutex
	convCancel  map[string]context.CancelFunc
	interjectMu sync.Mutex
	interject   map[string][]string
	cfg         config.Config
	a           *agent.Agent
	st          store.Store
	ho          *handoff.Manager
	hst         *store.SQLiteStore
	hk          *hook.Manager
}

func New(cfg config.Config, a *agent.Agent, st store.Store) *Server {
	s := &Server{cfg: cfg, a: a, st: st}
	logx.Init(filepath.Join(cfg.DataDir, ""))
	s.RunJobSweeper()
	s.RunAutoArchive()
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/shots/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// 只能服务 shots 子目录本身。原来这里 FileServer 的是整个 ~/.data/agentd，
		// 而 http.Dir 的 path.Clean 发生在 URL 解码之前 —— /shots/..%2Fagentd.db
		// 解码后变成 ../agentd.db，可以整个数据库（全部对话 + 配置）原样下载。
		dir := http.Dir(config.DataPath("shots"))
		http.StripPrefix("/shots/", http.FileServer(dir)).ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/trigger", s.handleTrigger)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, 405, map[string]string{"error": "GET only"})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "model": s.cfg.Model, "base_url": s.cfg.BaseURL})
	})
	mux.HandleFunc("/api/convs", s.handleConvs)
	mux.HandleFunc("/api/conv/", s.handleConv)
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/running", func(w http.ResponseWriter, r *http.Request) {
		s.convMu.Lock()
		ids := make([]string, 0, len(s.convCancel))
		for id := range s.convCancel {
			ids = append(ids, id)
		}
		s.convMu.Unlock()
		sort.Strings(ids)
		writeJSON(w, 200, ids)
	})
	mux.HandleFunc("/api/shots/cleanup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "POST only"})
			return
		}
		var in struct {
			Days int `json:"days"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Days <= 0 {
			in.Days = config.LoadSettings().ShotsKeepDays
		}
		n, bytes := s.cleanupShots(in.Days)
		writeJSON(w, 200, map[string]any{"removed": n, "freed_kb": bytes / 1024, "days": in.Days})
	})
	mux.HandleFunc("/api/shots/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, 405, map[string]string{"error": "GET only"})
			return
		}
		dir := config.DataPath("shots")
		ents, _ := os.ReadDir(dir)
		var n int
		var bytes int64
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil {
				n++
				bytes += info.Size()
			}
		}
		writeJSON(w, 200, map[string]any{"count": n, "size_mb": float64(bytes) / 1024 / 1024})
	})
	mux.HandleFunc("/api/context", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("conv")
		name := r.URL.Query().Get("profile")
		if name == "" {
			name = r.URL.Query().Get("p")
		}
		prof, ok := config.GetProfile(name)
		if !ok {
			writeJSON(w, 400, map[string]string{"error": "需要 profile"})
			return
		}
		ag := agent.New(s.cfg, s.st, prof)
		c, err := s.st.Get(id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": err.Error()})
			return
		}
		chars, max := ag.ContextSize(c)
		raw := 0 // 未压缩时的字符数（给人看真实体量）
		for _, m := range c.Messages {
			raw += len([]rune(m.Content)) + len([]rune(m.Reasoning))
		}
		pct := 0.0
		if max > 0 {
			pct = float64(chars) / float64(max) * 100
		}
		compactPct := ag.CompactPct()
		// 窗口与上限都以 token 表示；chars→tokens 用同一换算（1.8）
		win := ag.ContextWindow()
		estTokens := int(float64(chars) / 1.8)
		limitTokens := ag.MaxTokens()
		compactAt := limitTokens * compactPct / 100
		writeJSON(w, 200, map[string]any{"chars": chars, "max": max, "pct": pct, "raw": raw,
			"compact_pct": compactPct, "compact_at": compactAt,
			"est_tokens": estTokens, "window": win,
			"limit_tokens": limitTokens, "chars_per_token": 1.8,
			"messages": len(c.Messages), "summary": c.Summary != ""})
	})
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, 200, config.LoadSettings())
		case http.MethodPut, http.MethodPost:
			var in config.Settings
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			before := config.LoadSettings()
			if err := config.SaveSettings(in); err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			after := config.LoadSettings()
			// 只在"自动归档"相关配置变化时，立即巡检一次（其它配置保存不触发）
			if before.AutoArchiveEnabled != after.AutoArchiveEnabled ||
				before.AutoArchiveDays != after.AutoArchiveDays {
				logx.Info("settings.auto_archive enabled=%v days=%d", after.AutoArchiveEnabled, after.AutoArchiveDays)
				go s.sweepAutoArchive()
			}
			writeJSON(w, 200, after)
		default:
			writeJSON(w, 405, map[string]string{"error": "GET/PUT only"})
		}
	})
	mux.HandleFunc("/api/plugins", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			writeJSON(w, 405, map[string]string{"error": "POST/PUT only"})
			return
		}
		var d tools.PluginDraft
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := tools.SavePlugin(d); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if orig := strings.TrimSpace(d.OldName); orig != "" && orig != d.Name {
			if n, err := config.RenameToolInProfiles(orig, d.Name); err != nil {
				logx.Warn("plugin.rename sync profiles: %v", err)
			} else if n > 0 {
				logx.Info("plugin.rename %s -> %s, updated %d profile tools", orig, d.Name, n)
			}
		}
		logx.Info("plugin.save name=%s persistent=%v cmd=%v", d.Name, d.Persistent, d.Command)
		writeJSON(w, 200, map[string]any{"ok": true, "name": d.Name})
	})
	mux.HandleFunc("/api/plugins/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeJSON(w, 405, map[string]string{"error": "DELETE only"})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
		if err := tools.DeletePlugin(name); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		logx.Info("plugin.delete name=%s", name)
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/wake", s.handleWake)
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/tasks/", s.handleTasks)
	mux.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, 405, map[string]string{"error": "GET only"})
			return
		}
		reg := tools.Registry()
		out := make([]map[string]any, 0, len(reg))
		for _, sp := range reg {
			if sp.Name == "handoff_fail" {
				continue // 子会话专用，不进配置界面
			}
			// command / required 也要给前端：插件编辑表单靠它们回显，
			// 缺了就会拿一个"命令为空"的表单给你，一保存就被校验拦下。
			out = append(out, map[string]any{"name": sp.Name, "description": sp.Description,
				"params": sp.Params, "required": sp.Required, "command": sp.Command,
				"source": sp.Source, "path": sp.Path,
				"timeout_ms": sp.TimeoutMs, "persistent": sp.Persistent})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("/api/tools/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			writeJSON(w, 405, map[string]string{"error": "PUT only"})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/tools/")
		var body struct {
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if code, err := saveToolDescription(name, body.Description); err != nil {
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/apply", s.handleApply)
	mux.HandleFunc("/api/endpoints", s.handleEndpoints)
	// 精确路径优先于 /api/endpoints/ 前缀：草稿探测不写盘
	mux.HandleFunc("/api/endpoints/probe", s.handleEndpointProbe)
	mux.HandleFunc("/api/endpoints/", s.handleEndpointItem)
	mux.HandleFunc("/api/sources", s.handleSources)
	// 插件加载失败的说明单独一个只读端点：/api/tools 的响应是数组，
	// 加字段会破坏调用方；这里给界面一个"为什么某个插件不见了"的答案
	mux.HandleFunc("/api/tools/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, 405, map[string]string{"error": "GET only"})
			return
		}
		tools.Registry() // 刷新一次，保证读到当前目录的状态
		writeJSON(w, 200, map[string]any{"issues": tools.LoadWarnings()})
	})
	mux.HandleFunc("/api/profiles", s.handleProfiles)
	mux.HandleFunc("/api/profiles/", s.handleProfileItem)
	mux.HandleFunc("/", s.handleStatic)
	return secureHeaders(requireJSONBody(logReq(mux)))
}

// requireJSONBody：所有带 body 的写请求必须声明 application/json。
//
// 起因是一个能直接利用的 CSRF：浏览器里任何页面都可以
//
//	fetch('http://127.0.0.1:18400/api/chat', {
//	  method: 'POST', headers: {'Content-Type': 'text/plain'},
//	  body: '{"conv_id":"51","text":"随便干点什么"}'})
//
// text/plain 属于 CORS 的「简单请求」，**不触发预检**，请求会照常送达 ——
// 后端只认 body、不看 Content-Type，于是照样解析、照样执行。
// 响应因为没给 CORS 头而读不到，可副作用已经发生（这一轮真的会跑，模型能执行命令）。
//
// 要求 application/json 就能堵死：浏览器发这个 Content-Type 必然先发 OPTIONS 预检，
// 而不给 CORS 头的服务端不会放行预检。
//
// ContentLength != 0 而不是 > 0：chunked 传输是 -1，一并要求。
// 没有 body 的 POST（比如停止生成）照旧放行。
func requireJSONBody(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			if r.ContentLength != 0 {
				ct := strings.ToLower(r.Header.Get("Content-Type"))
				if !strings.HasPrefix(ct, "application/json") {
					writeJSON(w, 415, map[string]string{
						"error": "写请求必须使用 Content-Type: application/json"})
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}

// secureHeaders：几个零误伤的安全响应头。
//   - X-Frame-Options: 这个界面没有嵌入需求，却能被打包成 iframe 做点击劫持
//     （透明浮层盖在「删除插件」上）。DENY 不影响自己打开。
//   - nosniff: 防止浏览器把 JSON 响应猜成 HTML/CSS 去执行。
//   - Referrer-Policy: URL 里带 conv id，不要带给第三方。
//
// 刻意不加 CORS 头：跨站读不到任何响应，这正是想要的。
func secureHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

func logReq(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/" {
			ua := r.UserAgent()
			if len(ua) > 28 {
				ua = ua[:28]
			}
			fmt.Fprintf(os.Stderr, "[web] %s %s ua=%q\n", r.Method, r.URL.Path, ua)
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleConvs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]string{"error": "GET only"})
		return
	}
	var list []*store.Conversation
	var err error
	if r.URL.Query().Get("archived") == "1" {
		list, err = s.st.ListArchived()
	} else {
		list, err = s.st.List()
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	type row struct {
		ID        string    `json:"ID"`
		Title     string    `json:"Title"`
		UpdatedAt time.Time `json:"UpdatedAt"`
		N         int       `json:"N"`
		ParentID  int64     `json:"ParentID"`
		Kind      string    `json:"Kind"`
		Seq       int       `json:"Seq"` // 在父会话下的序号（↳#N）
	}
	// 子会话序号：按 id 升序在兄弟间编号
	seqOf := map[string]int{}
	byParent := map[int64][]*store.Conversation{}
	for _, c := range list {
		if c.ParentID > 0 {
			byParent[c.ParentID] = append(byParent[c.ParentID], c)
		}
	}
	for _, kids := range byParent {
		// 按数值排，不能按字符串：id 是从 SQLite 的自增列来的，跨位数之后
		// "1000" < "999" 成立，于是后建的子会话会拿到更小的序号 ——
		// 左栏的 ↳#N 就指向了错的会话。非数字 id 退回字符串比较兜底。
		sort.Slice(kids, func(i, j int) bool {
			a, ea := strconv.ParseInt(kids[i].ID, 10, 64)
			b, eb := strconv.ParseInt(kids[j].ID, 10, 64)
			if ea == nil && eb == nil {
				return a < b
			}
			return kids[i].ID < kids[j].ID
		})
		for i, k := range kids {
			seqOf[k.ID] = i + 1
		}
	}
	out := make([]row, 0, len(list))
	for _, c := range list {
		n := c.MsgCount
		if n == 0 {
			n = len(c.Messages)
		}
		out = append(out, row{c.ID, c.Title, c.UpdatedAt, n, c.ParentID, c.Kind, seqOf[c.ID]})
	}
	writeJSON(w, 200, out)
}

func (s *Server) pushInterject(convID, text string) {
	s.interjectMu.Lock()
	defer s.interjectMu.Unlock()
	if s.interject == nil {
		s.interject = map[string][]string{}
	}
	s.interject[convID] = append(s.interject[convID], text)
}

func (s *Server) drainInterject(convID string) []string {
	s.interjectMu.Lock()
	defer s.interjectMu.Unlock()
	q := s.interject[convID]
	delete(s.interject, convID)
	return q
}

// isShotPlaceholder 判断“（上一个工具的截图，请以图为准）”这类占位用户消息。
// 角色判断放在函数里而不是交给调用方：原来只看图和前缀，assistant 消息只要带了图
// 也会被判成占位消息 —— convWindow 是靠外层先判 role 才没出事。
func isShotPlaceholder(m *store.Message) bool {
	return m.Role == "user" && len(m.Images) > 0 && strings.HasPrefix(m.Content, "（上一个工具的截图")
}

// maxPageMsgs 是一次分页返回的消息条数上限。超过就按轮二分降级；
// 降到 1 轮仍超限时宁可超限，也不能把一轮切成两半（否则会出现“没有提问的工具轮”）。
const maxPageMsgs = 400

// legacySysNote 兼容旧数据：加 status 列之前，系统提示是靠这些前缀写在正文里的。
var legacySysNote = regexp.MustCompile(`^\s*(——)?\s*（(已达到工具轮次上限|已停止|中断|检测到连续|任务回报|任务已派发|进程中断)`)

// classifyMessage 给每条消息算一个派生 kind（不落库），让前端不用再猜。
//
//	sysnote     独立系统提示（timer / handoff / 空壳中断 / 旧数据前缀）
//	interrupted 有正文的中断（前端加角标，正文照常渲染）
//	failed      有正文的失败
//	streaming   流式生成中
//	""          普通消息
func classifyMessage(m *store.Message) string {
	if m.Origin == "timer" || m.Origin == "handoff" {
		return "sysnote"
	}
	// 纯说明（“（已停止）”“（中断：…）”）不管什么状态都当系统提示：
	// 它们不是正文，不该按“有正文的中断”渲染。
	if strings.TrimSpace(m.Reasoning) == "" && legacySysNote.MatchString(m.Content) {
		return "sysnote"
	}
	switch m.Status {
	case "interrupted", "failed":
		return m.Status
	case "running":
		return "streaming"
	}
	return ""
}

// convWindow 按“轮”切窗口。轮 = 一条 user 消息 + 它后面所有 assistant/tool 消息。
// before <= 0：取最后 rounds 轮；否则取 seq < before 的最后一轮，再往前共 rounds 轮。
// 返回值 from/to 都是 1-based 轮号；hasMore 表示还有更早的轮没有加载。
func convWindow(all []store.Message, rounds, before, around int) (out []store.Message, from, to, total int, hasMore bool) {
	var users []int
	for i := range all {
		// 工具截图占位消息不是一次“用户提问”（前端 normalize 也把它过滤掉了），
		// 统计轮次时必须同样排除，否则 total_rounds 会比实际多，标尺多出一格。
		// 这里仍要自己判 role：isShotPlaceholder 内部的 role 判断是防别的调用方误用，
		// 和「必须是一条 user 消息」是两件事 —— 只靠它会把 assistant 也算成一轮。
		if all[i].Role == "user" && !isShotPlaceholder(&all[i]) {
			users = append(users, i)
		}
	}
	total = len(users)
	if total == 0 {
		return nil, 0, 0, 0, false
	}
	if rounds <= 0 {
		rounds = 1
	}
	last := total - 1
	var first int
	switch {
	case around > 0:
		// 跳到第 around 轮（1-based）：上面留 2 轮当前情，下面铺满其余
		first = around - 1 - 2
		if first < 0 {
			first = 0
		}
		last = first + rounds - 1
		if last > total-1 {
			last = total - 1
			first = last - rounds + 1
			if first < 0 {
				first = 0
			}
		}
	case before > 0:
		last = -1
		for k, idx := range users {
			if all[idx].Seq < before {
				last = k
			}
		}
		if last < 0 {
			return nil, 0, 0, total, false
		}
		first = last - rounds + 1
		if first < 0 {
			first = 0
		}
	default:
		first = last - rounds + 1
		if first < 0 {
			first = 0
		}
	}
	startIdx := users[first]
	endIdx := len(all)
	if last+1 < total {
		endIdx = users[last+1]
	}
	return all[startIdx:endIdx], first + 1, last + 1, total, first > 0
}

// pageConv 取一个窗口，并在超限时按轮二分降级（10 → 5 → 2 → 1）。
func pageConv(all []store.Message, rounds, before, around int) (out []store.Message, from, to, total int, hasMore bool, used int) {
	if rounds <= 0 {
		rounds = 10
	}
	n := rounds
	for n >= 1 {
		out, from, to, total, hasMore = convWindow(all, n, before, around)
		used = n
		if len(out) <= maxPageMsgs || n == 1 {
			return
		}
		n /= 2
	}
	return
}

func (s *Server) handleConv(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/api/conv/")
	if strings.HasSuffix(raw, "/stream") {
		s.streamFrom(w, r, strings.TrimSuffix(raw, "/stream"))
		return
	}
	if strings.HasSuffix(raw, "/stop") {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "POST only"})
			return
		}
		id := strings.TrimSuffix(raw, "/stop")
		s.convMu.Lock()
		cancel := s.convCancel[id]
		s.convMu.Unlock()
		if cancel == nil {
			writeJSON(w, 404, map[string]string{"error": "这一轮已经结束"})
			return
		}
		cancel()
		writeJSON(w, 200, map[string]any{"ok": true, "stopped": id})
		return
	}
	if strings.HasSuffix(raw, "/interject") {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "POST only"})
			return
		}
		id := strings.TrimSuffix(raw, "/interject")
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
			writeJSON(w, 400, map[string]string{"error": "需要 text"})
			return
		}
		// 没有正在跑的回合时，插话会被存进内存等待 drain，而永远不会有人来取 → 消息丢失。
		// 这时当作一条普通消息：落库并直接起一轮（用户按"继续"就是想要它接着做）。
		s.convMu.Lock()
		running := s.convCancel[id] != nil
		s.convMu.Unlock()
		if running {
			s.pushInterject(id, body.Text)
			writeJSON(w, 200, map[string]any{"ok": true, "mode": "interject"})
			return
		}
		if n, err := strconv.ParseInt(id, 10, 64); err == nil && s.ho != nil {
			// 先确认会话真的存在：runTurn 在 goroutine 里，会话读不到只会打一行
			// WARN 日志，而这里已经把 200 回给客户端了 —— 用户会以为"接着做"成功了。
			if _, err := s.st.Get(id); err != nil {
				writeJSON(w, 404, map[string]string{"error": "没有这个会话"})
				return
			}
			go func() {
				if err := s.runTurn(context.Background(), n, body.Text, "user"); err != nil {
					logx.Warn("interject->turn conv=%s: %v", id, err)
				}
			}()
			writeJSON(w, 200, map[string]any{"ok": true, "mode": "turn"})
			return
		}
		writeJSON(w, 400, map[string]any{"ok": false, "error": "会话不在运行且无法起新回合"})
		return
	}
	if strings.HasSuffix(raw, "/restore") && r.Method == http.MethodPost {
		id := strings.TrimSuffix(raw, "/restore")
		logx.Info("conv.restore id=%s", id)
		if err := s.st.Restore(id); err != nil {
			writeJSON(w, 404, map[string]string{"error": "恢复失败：" + err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "restored": id})
		return
	}
	id := raw
	if r.Method == http.MethodDelete {
		logx.Info("conv.archive id=%s", id)
		// 先把这一轮取消掉：否则会话删了，goroutine 还在烧 token，
		// 而且 Save 会往一个已经不存在的会话里写（静默失败，白跑）
		s.convMu.Lock()
		if cancel := s.convCancel[id]; cancel != nil {
			cancel()
		}
		s.convMu.Unlock()
		if err := s.st.Archive(id); err != nil {
			writeJSON(w, 404, map[string]string{"error": "归档失败：" + err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "archived": id})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]string{"error": "GET/DELETE only"})
		return
	}
	c, err := s.st.Get(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "没有这个会话"})
		return
	}
	for i := range c.Messages {
		c.Messages[i].Kind = classifyMessage(&c.Messages[i])
	}
	// 分段载入：?rounds=N[&before=<seq>]。不带参数时保持全量返回（兼容旧调用）。
	if r.URL.Query().Get("rounds") != "" {
		rounds, _ := strconv.Atoi(r.URL.Query().Get("rounds"))
		before, _ := strconv.Atoi(r.URL.Query().Get("before"))
		around, _ := strconv.Atoi(r.URL.Query().Get("around"))
		out, from, to, total, hasMore, used := pageConv(c.Messages, rounds, before, around)
		if out == nil {
			out = []store.Message{}
		}
		cc := *c
		cc.Messages = out
		type pageResp struct {
			*store.Conversation
			Page map[string]any `json:"page"`
		}
		remaining := 0
		if from > 0 {
			remaining = from - 1
		}
		lastSeq := 0
		if n := len(c.Messages); n > 0 {
			lastSeq = c.Messages[n-1].Seq
		}
		writeJSON(w, 200, pageResp{&cc, map[string]any{
			"rounds":           used,
			"total_rounds":     total,
			"from":             from,
			"to":               to,
			"remaining_rounds": remaining,
			"has_more":         hasMore,
			"messages":         len(out),
			"total_messages":   len(c.Messages),
			"last_seq":         lastSeq,
		}})
		return
	}
	writeJSON(w, 200, c)
}

// POST /api/chat {"conv_id":"","text":"..."}  → SSE: event: delta / event: done
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	var in struct {
		ConvID  string `json:"conv_id"`
		Text    string `json:"text"`
		Profile string `json:"profile"`
		// ReasoningEffort：本次请求临时覆盖 profile 的默认思考强度。
		// 不落盘 —— profile 里存的是"默认值"，界面上改的只是这一轮/这个会话。
		ReasoningEffort string `json:"reasoning_effort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeJSON(w, 400, map[string]string{"error": "需要 text"})
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		writeJSON(w, 500, map[string]string{"error": "不支持流式"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	var sendMu sync.Mutex
	var jb *job
	send := func(ev string, v any) {
		b, _ := json.Marshal(v)
		sendMu.Lock()
		defer sendMu.Unlock()
		frame := fmt.Sprintf("event: %s\ndata: %s\n\n", ev, b)
		if jb != nil {
			jb.emit(frame)
		}
		if _, err := io.WriteString(w, frame); err != nil {
			return // 连接已经没了，后面的帧也不用写了
		}
		// 用 ResponseController.Flush 而不是 flusher.Flush()：客户端断开后底层
		// bufio 会拿到 ErrShortWrite 并 panic（bufio.go:642），panic 会杀掉这个
		// 请求的 goroutine，于是这一轮留下一条 status=running 的僵尸消息、
		// 而且永远等不到 chat.done。ResponseController.Flush 只返回 error。
		_ = http.NewResponseController(w).Flush()
	}

	// 心跳：工具执行可能几十秒没有任何帧，不加心跳连接会被浏览器/系统判为空闲断掉 ✗
	// 只写注释帧 ": ping"，不会进入事件流解析（前端忽略）
	stopBeat := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopBeat:
				return
			case <-t.C:
				sendMu.Lock()
				if _, err := io.WriteString(w, ": ping\n\n"); err == nil {
					_ = http.NewResponseController(w).Flush()
				}
				sendMu.Unlock()
			}
		}
	}()
	defer close(stopBeat)

	ag := s.a
	set := config.LoadSettings()
	if set.ToolRoundsEnabled {
		ag.MaxRounds = set.ToolRounds
	}
	if s.ho != nil && strings.TrimSpace(in.ConvID) != "" {
		if pid, err := strconv.ParseInt(in.ConvID, 10, 64); err == nil {
			ag.SpawnTask = func(kind string, card map[string]any) (int64, error) { return s.ho.Dispatch(pid, kind, card) }
		}
	}
	ag.FailTask = func(reason, evidence string) (string, error) { return "（父会话无 handoff_fail）", nil }
	if in.Profile != "" {
		if prof, ok := config.GetProfile(in.Profile); ok {
			if e := strings.TrimSpace(in.ReasoningEffort); e != "" {
				prof.ReasoningEffort = e
			}
			ag = agent.New(s.cfg, s.st, prof)
			if s.ho != nil && strings.TrimSpace(in.ConvID) != "" {
				if pid, err := strconv.ParseInt(in.ConvID, 10, 64); err == nil {
					ag.SpawnTask = func(kind string, card map[string]any) (int64, error) { return s.ho.Dispatch(pid, kind, card) }
				}
			}
			if set.ToolRoundsEnabled {
				ag.MaxRounds = set.ToolRounds
			}
		}
	}
	ag.OnStats = func(st llm.Stats) {
		send("usage", map[string]any{
			"tps": st.PredictedPerSec, "completion": st.CompletionTokens,
			"prompt": st.PromptTokens, "cached": st.CachedTokens,
		})
	}
	c, err := ag.Conversation(in.ConvID)
	if err != nil {
		logx.Error("chat.error conv=%s err=%v", in.ConvID, err)
		send("error", map[string]string{"error": err.Error()})
		return
	}
	// 会话一建立就先落盘：即使这一轮被中断，会话本身与用户消息也不会丢。
	// 这里必须检查错误：新建会话时 ID 是 Save 分配的，写不进去 c.ID 就是空串 ——
	// 前端会收到 {"conv_id":""} 并把它当成"已创建"，而实际上什么都没有；
	// 服务端还会接着跑完一整轮模型（白烧），直到最后才报错。
	if err := s.st.Save(c); err != nil {
		logx.Error("chat.save conv=%q err=%v", c.ID, err)
		send("error", map[string]string{"error": "会话保存失败：" + err.Error()})
		return
	}
	jb = s.job(c.ID)
	jb.reset()
	defer jb.finish()
	send("meta", map[string]string{"conv_id": c.ID, "title": c.Title})

	// 中途插话：本请求期间可被 /api/conv/<id>/interject 追加
	convID := c.ID
	ag.Drain = func() []string { return s.drainInterject(convID) }
	ag.OnInterject = func(t string) { send("interject", map[string]string{"text": t}) }

	// 后台任务：**不绑 r.Context()** —— 浏览器刷新/断开不会中断这一轮
	ctx, cancel := context.WithCancel(context.Background())
	s.convMu.Lock()
	if s.convCancel == nil {
		s.convCancel = map[string]context.CancelFunc{}
	}
	// 同一会话同时只允许跑一轮。不拦的话两个请求会同时往后追加 user 消息，
	// 谁先 Save 谁赢 —— 实测消息顺序会颠倒（先发的落在后面），而且两边都会
	// 预分配 seq 占位，最后留下一条 status=running 的僵尸消息（没人再更新它）。
	// 顺带也保住了 convCancel[convID] 不被后一个请求覆盖（否则 /stop 只能停后一个）。
	if _, busy := s.convCancel[convID]; busy {
		s.convMu.Unlock()
		cancel()
		logx.Warn("chat.busy conv=%s 上一个回合还没结束，拒绝并发", convID)
		send("error", map[string]string{"error": "这个会话正在生成中，等它结束或先点停止"})
		return
	}
	s.convCancel[convID] = cancel
	s.convMu.Unlock()
	defer func() {
		s.convMu.Lock()
		delete(s.convCancel, convID)
		s.convMu.Unlock()
	}()
	start := time.Now()
	logx.Info("chat.start conv=%s profile=%s chars=%d", convID, ag.Profile().Name, len([]rune(in.Text)))
	ag.OnStage = func(stage string) { send("stage", map[string]string{"stage": stage}) }
	// 流式消息预分配到的 seq：前端可以把流式气泡挂到正确位置（不用等结束重拉）
	ag.OnSeq = func(seq int) { send("seq", map[string]int{"seq": seq}) }
	ag.OnToolStart = func(name, args string) {
		send("tool_start", map[string]any{"name": name, "args": args})
	}
	err = ag.SendWithTools(ctx, c, in.Text,
		func(content, reasoning string) {
			if content != "" {
				send("delta", map[string]string{"text": content})
			}
			if reasoning != "" {
				send("reasoning", map[string]string{"text": reasoning})
			}
		},
		func(name, args string, result any, ms int64) {
			text := ""
			hasImage := false
			if parts, ok := result.([]map[string]any); ok {
				for _, pt := range parts {
					if pt["type"] == "text" {
						if t, ok := pt["text"].(string); ok {
							text += t
						}
					}
					if pt["type"] == "image" {
						hasImage = true
					}
				}
			} else if str, ok := result.(string); ok {
				text = str
			}
			logx.Info("tool.call name=%s ms=%d image=%v out=%d", name, ms, hasImage, len([]rune(text)))
			send("tool", map[string]any{"name": name, "args": args, "text": text, "image": hasImage, "ms": ms})
		})
	if err != nil {
		logx.Error("chat.error conv=%s err=%v", in.ConvID, err)
		send("error", map[string]string{"error": err.Error()})
		return
	}
	// 结束时的上下文画像：便于事后判断"是不是快到上限/有没有压缩"
	chars, maxChars := ag.ContextSize(c)
	pct := 0.0
	if maxChars > 0 {
		pct = float64(chars) * 100 / float64(maxChars)
	}
	raw := 0
	for _, m := range c.Messages {
		raw += len([]rune(m.Content)) + len([]rune(m.Reasoning))
	}
	logx.Info("chat.done conv=%s secs=%.1f ctx=%d/%d(%.0f%%) raw=%d msgs=%d summary=%v",
		c.ID, time.Since(start).Seconds(), chars, maxChars, pct, raw, len(c.Messages), c.Summary != "")
	send("done", map[string]any{"conv_id": c.ID, "secs": time.Since(start).Seconds()})
}

// 静态文件：优先 embed（P4 会填 web/dist），没有就返回占位页。
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WebDir != "" {
		if _, err := os.Stat(s.cfg.WebDir + "/index.html"); err == nil {
			// 带 hash 的资源可以长缓存；index.html 必须不缓存，否则改了页面刷不出来
			if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
				w.Header().Set("Cache-Control", "no-store, must-revalidate")
			}
			http.FileServer(http.Dir(s.cfg.WebDir)).ServeHTTP(w, r)
			return
		}
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// 静态资源：dist 下的文件（/assets/*.js|css 等）；缺文件时给占位页
	if s.cfg.WebDir != "" {
		if _, err := os.Stat(s.cfg.WebDir + "/index.html"); err == nil {
			// 带 hash 的资源可以长缓存；index.html 必须不缓存，否则改了页面刷不出来
			if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
				w.Header().Set("Cache-Control", "no-store, must-revalidate")
			}
			http.FileServer(http.Dir(s.cfg.WebDir)).ServeHTTP(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, placeholder)
}

const placeholder = `<!doctype html><meta charset="utf-8"><title>agentd</title>
<style>body{background:#0f1115;color:#d7dae0;font:14px/1.6 ui-monospace,monospace;padding:2rem}</style>
<h3>agentd</h3><p>API 已就绪；前端还没构建（P4）。</p>
<ul><li>GET /api/health</li><li>GET /api/convs</li><li>POST /api/chat</li></ul>`

// GET/POST /api/profiles  ·  PUT/DELETE /api/profiles/<name>
// saveToolDescription：把工具说明写回它真正的来源。
// 插件写清单（否则会被 tools.json 覆盖，变成两份真源打架），内置写 tools.json 覆盖。
// 单独抽出来是因为批量提交也要走同一条路 —— 两处各写一套迟早会分叉。
func saveToolDescription(name, desc string) (int, error) {
	sp, ok := tools.Registry()[name]
	if !ok {
		return 404, fmt.Errorf("没有这个工具")
	}
	logx.Info("tool.save name=%s chars=%d source=%s", name, len([]rune(desc)), sp.Source)
	if sp.Source == "plugin" {
		b, _ := os.ReadFile(sp.Path)
		var m tools.PluginDraft
		_ = json.Unmarshal(b, &m)
		m.Name, m.Description = name, desc
		if err := tools.SavePlugin(m); err != nil {
			return 400, err
		}
		return 200, nil
	}
	if err := tools.SaveOverride(name, desc); err != nil {
		return 500, err
	}
	return 200, nil
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 附带解析后的模型信息：前端要知道当前配置用的是哪个上游模型、
		// 能不能吃图（决定要不要提示）、实际窗口多大。
		// 不塞进 Profile 本体，避免把"三层拼起来的结果"也存进配置文件。
		list := config.LoadProfiles()
		type view struct {
			config.Profile
			ModelID      string   `json:"model_id"`
			Input        []string `json:"input"`
			ResolvedCtx  int      `json:"resolved_context_window"`
			ResolvedOut  int      `json:"resolved_max_tokens"`
			EffortLevels []string `json:"effort_levels"`
		}
		out := make([]view, 0, len(list))
		for _, p := range list {
			r := p.Resolve()
			out = append(out, view{Profile: p, ModelID: r.ModelID, Input: r.Input,
				ResolvedCtx: r.ContextWindow, ResolvedOut: r.MaxTokens, EffortLevels: r.EffortLevels})
		}
		writeJSON(w, 200, out)
	case http.MethodPost, http.MethodPut:
		var p config.Profile
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := config.UpsertProfile(p); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "profile": p.Name})
	default:
		writeJSON(w, 405, map[string]string{"error": "GET/POST/PUT only"})
	}
}

func (s *Server) handleProfileItem(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/profiles/")
	switch r.Method {
	case http.MethodGet:
		if p, ok := config.GetProfile(name); ok {
			writeJSON(w, 200, p)
			return
		}
		writeJSON(w, 404, map[string]string{"error": "没有这个 profile"})
	case http.MethodDelete:
		if err := config.DeleteProfile(name); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 405, map[string]string{"error": "GET/DELETE only"})
	}
}

// sweepAutoArchive：按当前设置执行一次自动归档
// cleanupShots：删除 shots 目录里超过 days 天的截图，返回删除数量与释放字节
func (s *Server) cleanupShots(days int) (int, int64) {
	dir := config.DataPath("shots")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	var n int
	var bytes int64
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n++
			bytes += info.Size()
		}
	}
	if n > 0 {
		logx.Info("shots.cleanup removed=%d freed=%dKB days=%d", n, bytes/1024, days)
	}
	return n, bytes
}

// CancelAll：取消所有进行中的轮次（服务停机时调用，让循环把半截内容与中断说明落库）
func (s *Server) CancelAll() int {
	s.convMu.Lock()
	defer s.convMu.Unlock()
	n := 0
	for _, cancel := range s.convCancel {
		cancel()
		n++
	}
	return n
}

func (s *Server) sweepAutoArchive() {
	st := config.LoadSettings()
	if st.ShotsAutoClean && st.ShotsKeepDays > 0 {
		s.cleanupShots(st.ShotsKeepDays)
	}
	if !st.AutoArchiveEnabled || st.AutoArchiveDays <= 0 {
		return
	}
	before := time.Now().Add(-time.Duration(st.AutoArchiveDays) * 24 * time.Hour)
	if n, err := s.st.ArchiveInactiveBefore(before); err == nil && n > 0 {
		logx.Info("auto_archive archived=%d days=%d", n, st.AutoArchiveDays)
	}
}

// RunAutoArchive：按设置把长期不活跃的会话自动归档（启动时一次，之后每小时一次）
func (s *Server) RunAutoArchive() {
	go func() {
		tick := func() {
			s.sweepAutoArchive()
		}
		tick()
		for range time.Tick(time.Hour) {
			tick()
		}
	}()
}
