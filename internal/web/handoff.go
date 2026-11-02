package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentd/internal/agent"
	"agentd/internal/config"
	"agentd/internal/handoff"
	"agentd/internal/llm"
	"agentd/internal/logx"
	"agentd/internal/store"
)

// InitHandoff：装配交接/唤醒（只有 SQLite 存储支持）
func (s *Server) InitHandoff(dataDir string) error {
	hst, ok := s.st.(*store.SQLiteStore)
	if !ok {
		return nil // JSON 存储（测试/旧版）不启用
	}
	if n, err := hst.MarkTasksInterrupted(); err == nil && n > 0 {
		logx.Info("handoff: %d 个任务因重启标记为 interrupted", n)
	}
	m, err := handoff.New(hst, dataDir, handoff.Hooks{
		RunChild: func(ctx context.Context, parent int64, profile, card string, maxRounds int) (int64, string, error) {
			return s.runChild(ctx, parent, profile, card, maxRounds)
		},
		RunTurn: func(ctx context.Context, conv int64, text, origin string) error {
			return s.runTurn(ctx, conv, text, origin)
		},
	})
	if err != nil {
		return err
	}
	s.ho = m
	s.hst = hst
	go s.outboxLoop()
	return nil
}

func (s *Server) outboxLoop() {
	for {
		time.Sleep(1 * time.Second)
		if s.ho == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		if err := s.ho.ConsumeOutbox(ctx); err != nil {
			logx.Warn("outbox: %v", err)
		}
		cancel()
	}
}

func (s *Server) profileOf(c *store.Conversation) config.Profile {
	name := c.KV["profile"]
	if name != "" {
		if p, ok := config.GetProfile(name); ok {
			return p
		}
	}
	return s.a.Profile()
}

// runTurn：往会话投一条消息并跑一轮（outbox 投递也走这里）
func (s *Server) runTurn(ctx context.Context, conv int64, text, origin string) error {
	c, err := s.st.Get(fmt.Sprint(conv))
	if err != nil {
		return err
	}
	prof := s.profileOf(c)
	ag := agent.New(s.cfg, s.st, prof)
	if set := config.LoadSettings(); set.ToolRoundsEnabled {
		ag.MaxRounds = set.ToolRounds
	}
	ag.OnStats = func(llm.Stats) {}
	ag.MsgOrigin = origin
	err = ag.SendWithTools(ctx, c, text, func(string, string) {}, func(string, string, any, int64) {})
	_ = s.st.Save(c)
	return err
}

// runChild：建子会话并跑完一轮，返回子会话 id 与最后一段正文
func (s *Server) runChild(ctx context.Context, parent int64, profileName, card string, maxRounds int) (int64, string, error) {
	prof, ok := config.GetProfile(profileName)
	if !ok {
		return 0, "", fmt.Errorf("子会话 profile %q 不存在", profileName)
	}
	// 子会话工具固定：bash + handoff_fail（不受配方 profile 的 tools 影响）
	prof.Tools = []string{"bash", "handoff_fail"}
	c := &store.Conversation{Title: childTitle(card, parent), ParentID: parent, Kind: "child",
		KV: map[string]string{"profile": profileName}}
	if err := s.st.Save(c); err != nil {
		return 0, "", err
	}
	ag := agent.New(s.cfg, s.st, prof)
	ag.MsgOrigin = "handoff"
	card = humanCard(card)
	ag.MaxRounds = maxRounds
	ag.SpawnTask = nil // 子会话不能再派发任务（天然两层）
	ag.FailTask = func(reason, evidence string) (string, error) {
		_ = os.WriteFile(filepath.Join(os.TempDir(), fmt.Sprintf("agentd-child-%s.fail", c.ID)),
			[]byte(reason+"\n"+evidence), 0o644)
		return "已记录失败事实", nil
	}
	err := ag.SendWithTools(ctx, c, card, func(string, string) {}, func(string, string, any, int64) {})
	_ = s.st.Save(c)
	id, _ := strconv.ParseInt(c.ID, 10, 64)
	last := ""
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == "assistant" && strings.TrimSpace(c.Messages[i].Content) != "" {
			last = c.Messages[i].Content
			break
		}
	}
	if err != nil {
		return id, last, err
	}
	return id, last, nil
}

// ---------- HTTP ----------

func (s *Server) wakeToken() string {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".data", "agentd", "wake.token")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b))
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf) // 修复：os.ReadFile("/dev/urandom") 会读无限流卡死
	tok := fmt.Sprintf("%x", buf)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(tok+"\n"), 0o600)
	return tok
}

func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	if got := r.Header.Get("X-Agentd-Token"); got != s.wakeToken() {
		writeJSON(w, 401, map[string]string{"error": "token 不对（见 ~/.data/agentd/wake.token）"})
		return
	}
	if s.hst == nil {
		writeJSON(w, 503, map[string]string{"error": "交接未启用"})
		return
	}
	var in struct {
		ConvID string `json:"conv_id"`
		Text   string `json:"text"`
		Origin string `json:"origin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeJSON(w, 400, map[string]string{"error": "需要 text"})
		return
	}
	conv, err := strconv.ParseInt(in.ConvID, 10, 64)
	if err != nil || conv <= 0 {
		writeJSON(w, 400, map[string]string{"error": "需要 conv_id"})
		return
	}
	if in.Origin == "" {
		in.Origin = "system"
	}
	id, err := s.hst.CreateOutbox(conv, in.Origin, in.Text)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	logx.Info("wake: conv=%d outbox=%d origin=%s", conv, id, in.Origin)
	writeJSON(w, 200, map[string]any{"ok": true, "outbox_id": id})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "GET/POST only"})
		return
	}
	if s.hst == nil {
		writeJSON(w, 200, []any{})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks")
	if (path == "" || path == "/") && r.Method == http.MethodPost {
		// 手动派发（前端"新建任务"/脚本用）：{kind, conv_id, card}
		var in struct {
			Kind   string         `json:"kind"`
			ConvID string         `json:"conv_id"`
			Card   map[string]any `json:"card"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Kind == "" {
			writeJSON(w, 400, map[string]string{"error": "需要 kind"})
			return
		}
		parent, _ := strconv.ParseInt(in.ConvID, 10, 64)
		if parent <= 0 {
			writeJSON(w, 400, map[string]string{"error": "需要 conv_id"})
			return
		}
		if s.ho == nil {
			writeJSON(w, 503, map[string]string{"error": "未启用"})
			return
		}
		id, err := s.ho.Dispatch(parent, in.Kind, in.Card)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "task_id": id})
		return
	}
	if path == "" || path == "/" {
		if s.ho != nil {
			_ = s.ho.Load() // 配方改完即生效（列表也要看到）
		}
		list, _ := s.hst.ListTasks()
		recipes := []string{}
		if s.ho != nil {
			recipes = s.ho.Names()
		}
		writeJSON(w, 200, map[string]any{"tasks": list, "recipes": recipes, "active": s.ho.Active()})
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	t, err := s.hst.GetTask(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "没有这个任务"})
		return
	}
	steps, _ := s.hst.Steps(id)
	obs, _ := s.hst.OutboxForConv(t.ParentConv, 20)
	if len(parts) == 1 {
		writeJSON(w, 200, map[string]any{"task": t, "steps": steps, "outbox": obs})
		return
	}
	switch parts[1] {
	case "restore":
		if s.ho == nil {
			writeJSON(w, 503, map[string]string{"error": "未启用"})
			return
		}
		if err := s.ho.Restore(id); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 404, map[string]string{"error": "未知操作"})
	}
}

// humanCard：把交接卡 JSON 变成给子会话读的自然语言（别把原始 JSON 塞进对话）
func humanCard(raw string) string {
	var d struct {
		Kind string         `json:"kind"`
		Card map[string]any `json:"card"`
	}
	if json.Unmarshal([]byte(raw), &d) != nil {
		return raw
	}
	var b strings.Builder
	b.WriteString("你是执行子会话。按下面的交接卡完成任务；完成后用一两句话汇报结果（做了什么、产物在哪）。\n\n")
	if g, ok := d.Card["goal"].(string); ok && g != "" {
		b.WriteString("目标：" + g + "\n")
	}
	for k, v := range d.Card {
		if k == "goal" {
			continue
		}
		b.WriteString(k + "：" + fmt.Sprint(v) + "\n")
	}
	b.WriteString("\n工具只有 bash（以及 handoff_fail：无法继续时只写事实）。")
	return b.String()
}

// childTitle：用交接卡目标当标题（截断），拿不到就退回"↳ 父 #N"
func childTitle(card string, parent int64) string {
	var d struct {
		Kind string         `json:"kind"`
		Card map[string]any `json:"card"`
	}
	if json.Unmarshal([]byte(card), &d) == nil {
		if g, _ := d.Card["goal"].(string); strings.TrimSpace(g) != "" {
			r := []rune(strings.TrimSpace(g))
			if len(r) > 24 {
				r = append(r[:24], '…')
			}
			return string(r)
		}
		if d.Kind != "" {
			return d.Kind + " · 父 No." + fmt.Sprint(parent)
		}
	}
	return "子会话 · 父 No." + fmt.Sprint(parent)
}
