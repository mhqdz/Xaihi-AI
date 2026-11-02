// Package handoff：交接与唤醒（配方执行 + 子会话 + outbox 投递）
package handoff

import (
	"agentd/internal/config"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentd/internal/store"
)

type Step struct {
	Run   string `json:"run"`
	Spawn bool   `json:"spawn"`
}

type Recipe struct {
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Profile     string   `json:"profile"`
	MaxRounds   int      `json:"max_rounds"`
	TimeoutS    int      `json:"timeout_s"`
	Steps       []Step   `json:"steps"`
	Restore     []Step   `json:"restore"`
	Artifacts   []string `json:"artifacts"`
}

type Hooks struct {
	// RunChild：建子会话并跑完一轮，返回子会话 id 与最后一段正文
	RunChild func(ctx context.Context, parent int64, profile, card string, maxRounds int) (int64, string, error)
	// RunTurn：往某个会话投一条消息并跑一轮（outbox 投递）
	RunTurn func(ctx context.Context, conv int64, text, origin string) error
}

type Manager struct {
	st      *store.SQLiteStore
	Dir     string // ~/.data/agentd/handoff
	Recipes map[string]Recipe
	hooks   Hooks

	mu     sync.Mutex
	active map[int64]context.CancelFunc
	busy   map[int64]bool // 会话正在投递 outbox
}

func New(st *store.SQLiteStore, dataDir string, h Hooks) (*Manager, error) {
	m := &Manager{st: st, Dir: filepath.Join(dataDir, "handoff"), Recipes: map[string]Recipe{},
		hooks: h, active: map[int64]context.CancelFunc{}, busy: map[int64]bool{}}
	_ = os.MkdirAll(m.Dir, 0o755)
	if err := m.Load(); err != nil {
		return nil, err
	}
	return m, nil
}

// Load：读默认配方目录
func (m *Manager) Load() error {
	return m.LoadDir(config.DataPath("tasks.d"))
}

// LoadDir：读指定目录的配方（测试用）
func (m *Manager) LoadDir(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil // 没有配方目录是允许的
	}
	out := map[string]Recipe{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r Recipe
		if json.Unmarshal(b, &r) != nil || r.Kind == "" || len(r.Steps) == 0 {
			continue
		}
		out[r.Kind] = r
	}
	m.Recipes = out
	return nil
}

func (m *Manager) Names() []string {
	out := make([]string, 0, len(m.Recipes))
	for k := range m.Recipes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Dispatch：建任务行并异步执行
func (m *Manager) Dispatch(parent int64, kind string, card map[string]any) (int64, error) {
	r, ok := m.Recipes[kind]
	if !ok {
		_ = m.Load() // 配方改完即生效（与插件一致）
		r, ok = m.Recipes[kind]
	}
	if !ok {
		return 0, fmt.Errorf("未注册的任务类型 %q（可用：%s）", kind, strings.Join(m.Names(), ", "))
	}
	cb, _ := json.MarshalIndent(map[string]any{"kind": kind, "card": card, "created_at": time.Now().Format(time.RFC3339)}, "", "  ")
	tid, err := m.st.CreateTask(kind, parent, "")
	if err != nil {
		return 0, err
	}
	path := filepath.Join(m.Dir, fmt.Sprintf("task-%d.json", tid))
	_ = os.WriteFile(path, cb, 0o644)
	go m.run(tid, r, parent, string(cb))
	return tid, nil
}

func (m *Manager) run(tid int64, r Recipe, parent int64, card string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(maxOr(r.TimeoutS, 3600))*time.Second)
	m.mu.Lock()
	m.active[tid] = cancel
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.active, tid)
		m.mu.Unlock()
	}()

	var steps []store.TaskStep
	seq := 0
	fail := ""
	var childID int64
	var childText string

	push := func(s store.TaskStep) {
		seq++
		s.Seq = seq
		steps = append(steps, s)
		_ = m.st.AddStep(tid, s)
	}

	for _, stp := range r.Steps {
		if stp.Spawn {
			profile := r.Profile
			if profile == "" {
				profile = "9-exec"
			}
			t0 := time.Now()
			cid, text, err := m.hooks.RunChild(ctx, parent, profile, card, maxOr(r.MaxRounds, 20))
			st := store.TaskStep{Name: "spawn", Kind: "spawn", Cmd: "spawn child", Ms: time.Since(t0).Milliseconds(), Status: "ok"}
			if err != nil {
				st.Status, st.Output = "failed", trunc(err.Error(), 2000)
				fail = "子会话失败：" + err.Error()
			} else {
				childID, childText = cid, text
			}
			if cid > 0 {
				_ = m.st.SetTaskChild(tid, cid) // 失败也要记子会话指针
				childID = cid
				st.Output = fmt.Sprintf("child #%d", cid)
				st.Cmd = fmt.Sprintf("child #%d (%s)", cid, profile)
			}
			push(st)
			if fail != "" {
				break
			}
			continue
		}
		if strings.TrimSpace(stp.Run) == "" {
			continue
		}
		t0 := time.Now()
		code, out := runCmd(ctx, stp.Run, time.Duration(maxOr(r.TimeoutS, 600))*time.Second)
		st := store.TaskStep{Name: trunc(stp.Run, 40), Kind: "run", Cmd: stp.Run, ExitCode: code,
			OutChars: len([]rune(out)), Ms: time.Since(t0).Milliseconds(), Status: "ok", Output: trunc(out, 2000)}
		if code != 0 {
			st.Status = "failed"
			fail = fmt.Sprintf("步骤失败（exit %d）：%s", code, strings.TrimSpace(trunc(out, 300)))
		}
		push(st)
		if fail != "" {
			break
		}
	}

	// 产物检查
	artFail := ""
	if fail == "" && len(r.Artifacts) > 0 {
		missing := []string{}
		for _, pat := range r.Artifacts {
			ms, _ := filepath.Glob(expand(pat))
			if len(ms) == 0 {
				missing = append(missing, pat)
			}
		}
		st := store.TaskStep{Name: "artifacts", Kind: "artifact", Cmd: strings.Join(r.Artifacts, " "), Status: "ok"}
		if len(missing) > 0 {
			st.Status = "failed"
			st.Output = "缺少：" + strings.Join(missing, ", ")
			artFail = "产物缺失：" + strings.Join(missing, ", ")
		} else {
			st.Output = "全部存在"
		}
		push(st)
	}

	// restore：无论成败都跑，失败重试 3 次
	restoreErr := ""
	for _, stp := range r.Restore {
		if strings.TrimSpace(stp.Run) == "" {
			continue
		}
		var code int
		var out string
		for attempt := 0; attempt < 3; attempt++ {
			code, out = runCmd(context.Background(), stp.Run, 300*time.Second)
			if code == 0 {
				break
			}
			time.Sleep(2 * time.Second)
		}
		st := store.TaskStep{Name: "restore: " + trunc(stp.Run, 30), Kind: "restore", Cmd: stp.Run, ExitCode: code,
			OutChars: len([]rune(out)), Status: "ok", Output: trunc(out, 1000)}
		if code != 0 {
			st.Status = "failed"
			restoreErr = fmt.Sprintf("收尾失败：%s（exit %d）", stp.Run, code)
		}
		push(st)
	}

	status := "done"
	msg := fail
	if msg == "" && artFail != "" {
		msg = artFail
	}
	if msg != "" || restoreErr != "" {
		status = "failed"
	}
	if restoreErr != "" {
		status = "restore_failed"
		msg = strings.TrimSpace(msg + "；" + restoreErr)
	}

	rec := m.record(tid, r, steps, childID, childText, msg, restoreErr)
	_, _ = m.st.CreateOutbox(parent, "handoff", rec)
	_ = m.st.FinishTask(tid, status, msg)
}

func (m *Manager) record(tid int64, r Recipe, steps []store.TaskStep, child int64, childText, errMsg, restoreErr string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "（任务回报）%s #%d · %s\n", r.Kind, tid, map[bool]string{true: "成功", false: "失败"}[errMsg == "" && restoreErr == ""])
	for _, s := range steps {
		if s.Kind == "run" {
			fmt.Fprintf(&b, "· %s → exit %d · %dms · %d 字\n", trunc(s.Cmd, 60), s.ExitCode, s.Ms, s.OutChars)
		} else {
			fmt.Fprintf(&b, "· %s → %s %dms\n", s.Name, s.Status, s.Ms)
		}
	}
	if child > 0 {
		fmt.Fprintf(&b, "子会话：↳#%d\n", child)
		if strings.TrimSpace(childText) != "" {
			fmt.Fprintf(&b, "子会话结尾：%s\n", trunc(childText, 500))
		}
	}
	if errMsg != "" {
		fmt.Fprintf(&b, "失败原因：%s\n", trunc(errMsg, 500))
	}
	if restoreErr != "" {
		fmt.Fprintf(&b, "!! %s\n", trunc(restoreErr, 300))
	}
	return b.String()
}

// ConsumeOutbox：把 pending 消息按会话 FIFO 投递（一条投完再下一条同会话的）
func (m *Manager) ConsumeOutbox(ctx context.Context) error {
	items, err := m.st.PendingOutbox()
	if err != nil {
		return err
	}
	for _, o := range items {
		m.mu.Lock()
		if m.busy[o.ConvID] {
			m.mu.Unlock()
			continue
		}
		m.busy[o.ConvID] = true
		m.mu.Unlock()

		if m.st.ParentOf(o.ConvID) > 0 {
			m.st.RestoreArchived(o.ConvID)
		}
		err := m.hooks.RunTurn(ctx, o.ConvID, o.Text, o.Origin)
		status := "delivered"
		if err != nil {
			status = "failed"
		}
		_ = m.st.SetOutboxStatus(o.ID, status)

		m.mu.Lock()
		m.busy[o.ConvID] = false
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// Restore：手动执行收尾（interrupted 任务用）
func (m *Manager) Restore(tid int64) error {
	r, ok := m.Recipes[""] // placeholder
	_ = r
	t, err := m.st.GetTask(tid)
	if err != nil {
		return err
	}
	rec, ok := m.Recipes[t.Kind]
	if !ok {
		return fmt.Errorf("配方 %s 不存在", t.Kind)
	}
	var outs []string
	for _, stp := range rec.Restore {
		if strings.TrimSpace(stp.Run) == "" {
			continue
		}
		code, out := runCmd(context.Background(), stp.Run, 300*time.Second)
		outs = append(outs, fmt.Sprintf("%s → exit %d：%s", stp.Run, code, trunc(out, 200)))
	}
	_ = m.st.FinishTask(tid, "failed", "人工收尾："+strings.Join(outs, "；"))
	return nil
}

func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

func runCmd(ctx context.Context, cmd string, timeout time.Duration) (int, string) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(cctx, "bash", "-c", cmd)
	out, err := c.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return code, string(out)
}

func trunc(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func maxOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
