// Package watch：常驻观察器——"她现在在用什么应用"。
//
// 它只做两件事：采样、记事件。**不调模型、不主动说话**（那是会话层的事）。
// 这样成本可控：hyprctl 每 5 秒问一次，模型一天只在"值得说"的时候被叫醒几次。
//
// 为什么事件不进记忆：一天几百条（切窗口、关应用），进记忆会把 6000 token 的
// 预算吃光，把真正的偏好挤掉。事件进 SQLite，需要时再把最近一段摘要成一行。
package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// 事件类型
const (
	KindSwitch = "app_switch" // 稳定地切到了另一个窗口
	KindClose  = "app_close"  // 一个窗口消失了
)

// Event 是一条观察记录
type Event struct {
	ID    int64     `json:"id"`
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	App   string    `json:"app"`
	Title string    `json:"title"`
	PID   int       `json:"pid"`
}

// Win 是一个顶层窗口
type Win struct {
	Class string `json:"class"`
	Title string `json:"title"`
	PID   int    `json:"pid"`
}

// Default：进程级的观察库（main 启动时设置）。时间感要读它，
// 每个 agent 实例各开一次连接没必要。
var Default *Store

// Store 存事件与配额（独立 db 文件，不碰主库 schema）
type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := execMkdirAll(filepath.Dir(path)); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS watch_events(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		at TEXT NOT NULL, kind TEXT NOT NULL, app TEXT, title TEXT, pid INT);
		CREATE INDEX IF NOT EXISTS watch_events_at ON watch_events(at);
		CREATE TABLE IF NOT EXISTS watch_quota(day TEXT PRIMARY KEY, shots INT NOT NULL DEFAULT 0);`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Add 记一条事件
func (s *Store) Add(e Event) error {
	_, err := s.db.Exec(`INSERT INTO watch_events(at,kind,app,title,pid) VALUES(?,?,?,?,?)`,
		e.At.Format(time.RFC3339), e.Kind, e.App, e.Title, e.PID)
	return err
}

// Recent 取最近 d 内的事件（按时间升序）
func (s *Store) Recent(d time.Duration) ([]Event, error) {
	since := time.Now().Add(-d).Format(time.RFC3339)
	rows, err := s.db.Query(`SELECT id,at,kind,app,title,pid FROM watch_events WHERE at>=? ORDER BY at`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.App, &e.Title, &e.PID); err != nil {
			continue
		}
		e.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, e)
	}
	return out, nil
}

// TakeShot 占用一次截图配额：还有额度就 +1 并返回 true。
// 配额按天算、跨重启保留（放在库里，不放在内存里）。
func (s *Store) TakeShot(max int) (bool, int, error) {
	day := time.Now().Format("2006-01-02")
	if _, err := s.db.Exec(`INSERT INTO watch_quota(day,shots) VALUES(?,0)
		ON CONFLICT(day) DO NOTHING`, day); err != nil {
		return false, 0, err
	}
	res, err := s.db.Exec(`UPDATE watch_quota SET shots=shots+1 WHERE day=? AND shots<?`, day, max)
	if err != nil {
		return false, 0, err
	}
	n, _ := res.RowsAffected()
	var used int
	_ = s.db.QueryRow(`SELECT shots FROM watch_quota WHERE day=?`, day).Scan(&used)
	return n == 1, used, nil
}

// ShotsToday 今天已经用了多少张
func (s *Store) ShotsToday() int {
	var used int
	_ = s.db.QueryRow(`SELECT shots FROM watch_quota WHERE day=?`,
		time.Now().Format("2006-01-02")).Scan(&used)
	return used
}

// ---------- 采样 ----------

// Sampler 抽出来是为了测试能用假的（真实现只有 hyprctl 一种）
type Sampler interface {
	Active() (Win, error)
	Windows() ([]Win, error)
}

// Hypr 调 hyprctl。systemd --user 环境里已有 HYPRLAND_INSTANCE_SIGNATURE，
// 不用在这里猜 socket 路径。
type Hypr struct{}

func (Hypr) Active() (Win, error) {
	out, err := exec.Command("hyprctl", "activewindow", "-j").Output()
	if err != nil {
		return Win{}, err
	}
	var w struct {
		Class string `json:"class"`
		Title string `json:"title"`
		PID   int    `json:"pid"`
	}
	if err := json.Unmarshal(out, &w); err != nil {
		return Win{}, err
	}
	return Win{Class: w.Class, Title: w.Title, PID: w.PID}, nil
}

func (Hypr) Windows() ([]Win, error) {
	out, err := exec.Command("hyprctl", "clients", "-j").Output()
	if err != nil {
		return nil, err
	}
	var ws []struct {
		Class string `json:"class"`
		Title string `json:"title"`
		PID   int    `json:"pid"`
	}
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, err
	}
	res := make([]Win, 0, len(ws))
	for _, w := range ws {
		res = append(res, Win{Class: w.Class, Title: w.Title, PID: w.PID})
	}
	return res, nil
}

// ---------- 观察器 ----------

type Watcher struct {
	Store    *Store
	Samp     Sampler
	Interval time.Duration // 采样间隔，默认 5s
	Stable   time.Duration // 窗口要停多久才算"在做什么"，默认 30s

	// OnClose：窗口消失时回调（由 main 接到触发器上）。
	// 观察器自己**不做判断**——它只把事实交出去，要不要叫醒由钩子定义和配额决定。
	OnClose func(Win)

	mu      sync.Mutex
	lastKey string // 上次上报的 (class|title)
	curKey  string // 当前正在稳定的 (class|title)
	curWin  Win
	curFrom time.Time
}

func (w *Watcher) Run(ctx context.Context) {
	if w.Interval <= 0 {
		w.Interval = 5 * time.Second
	}
	if w.Stable <= 0 {
		w.Stable = 30 * time.Second
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	w.tick() // 先采一次，别等到第一个间隔
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick()
		}
	}
}

// tick 跑一次：稳定切换上报、窗口消失上报。
func (w *Watcher) tick() {
	win, err := w.Samp.Active()
	if err != nil {
		return // 拿不到就跳过这一轮（锁屏、切 tty、hyprctl 抖动都算正常）
	}
	key := win.Class + "|" + win.Title
	w.mu.Lock()
	if key != w.curKey {
		w.curKey, w.curWin, w.curFrom = key, win, time.Now()
	}
	stable := key == w.curKey && time.Since(w.curFrom) >= w.Stable
	needSwitch := stable && key != w.lastKey
	if needSwitch {
		w.lastKey = key
	}
	w.mu.Unlock()

	if needSwitch {
		_ = w.Store.Add(Event{At: time.Now(), Kind: KindSwitch, App: win.Class, Title: win.Title, PID: win.PID})
	}
}

// Closed 对比两次窗口列表，返回消失的窗口（按 class|pid 判断）。
// 单独抽出来是因为 tick 里做采样、这里可以按更长的间隔跑（默认 30s）。
func Closed(prev, cur []Win) []Win {
	now := map[string]bool{}
	for _, w := range cur {
		now[fmt.Sprintf("%s|%d", w.Class, w.PID)] = true
	}
	var gone []Win
	for _, w := range prev {
		if !now[fmt.Sprintf("%s|%d", w.Class, w.PID)] {
			gone = append(gone, w)
		}
	}
	return gone
}

// WatchWindows 每 Interval 对比一次窗口列表，把消失的记成 app_close。
// 和 Run 分开：窗口表比活动窗口贵一点，不需要 5 秒一次。
func (w *Watcher) WatchWindows(ctx context.Context) {
	interval := w.Interval * 6
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	prev, _ := w.Samp.Windows()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur, err := w.Samp.Windows()
			if err != nil {
				continue
			}
			for _, g := range Closed(prev, cur) {
				_ = w.Store.Add(Event{At: time.Now(), Kind: KindClose, App: g.Class, Title: g.Title, PID: g.PID})
				if w.OnClose != nil {
					w.OnClose(g)
				}
			}
			prev = cur
		}
	}
}

// Summary 把最近一段事件压成几行（给会话层临时用，不写进记忆）。
func Summary(es []Event) string {
	if len(es) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range es {
		switch e.Kind {
		case KindSwitch:
			fmt.Fprintf(&b, "%s 切到 %s（%s）\n", e.At.Format("15:04"), e.App, e.Title)
		case KindClose:
			fmt.Fprintf(&b, "%s 关掉 %s（%s）\n", e.At.Format("15:04"), e.App, e.Title)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
