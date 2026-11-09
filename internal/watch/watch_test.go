package watch

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 假采样器：按脚本返回活动窗口
type fakeSampler struct {
	active []Win
	win    [][]Win
	i, j   int
}

func (f *fakeSampler) Active() (Win, error) {
	if len(f.active) == 0 {
		return Win{}, nil
	}
	w := f.active[f.i]
	if f.i < len(f.active)-1 {
		f.i++
	}
	return w, nil
}

func (f *fakeSampler) Windows() ([]Win, error) {
	if len(f.win) == 0 {
		return nil, nil
	}
	w := f.win[f.j]
	if f.j < len(f.win)-1 {
		f.j++
	}
	return w, nil
}

func newWatcher(t *testing.T, s Sampler, stable time.Duration) *Watcher {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "watch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Watcher{Store: st, Samp: s, Interval: time.Millisecond, Stable: stable}
}

// 窗口停满 Stable 才上报一次，并且同样的窗口不重复上报
func TestTickReportsStableOnce(t *testing.T) {
	f := &fakeSampler{active: []Win{{Class: "firefox", Title: "文档", PID: 1}}}
	w := newWatcher(t, f, 0) // Stable=0：第一次 tick 就算稳定
	w.tick()
	w.tick()
	w.tick()
	es, _ := w.Store.Recent(time.Hour)
	if len(es) != 1 || es[0].Kind != KindSwitch || es[0].App != "firefox" {
		t.Fatalf("应该只上报一次，得到 %+v", es)
	}
}

// 没停够时间不上报
func TestTickWaitsForStable(t *testing.T) {
	f := &fakeSampler{active: []Win{{Class: "a", Title: "x", PID: 1}}}
	w := newWatcher(t, f, time.Hour)
	w.tick()
	if es, _ := w.Store.Recent(time.Hour); len(es) != 0 {
		t.Fatalf("还不该上报：%+v", es)
	}
}

// 换窗口 → 新的一条
func TestTickSwitch(t *testing.T) {
	f := &fakeSampler{active: []Win{
		{Class: "firefox", Title: "a", PID: 1},
		{Class: "kitty", Title: "b", PID: 2},
	}}
	w := newWatcher(t, f, 0)
	w.tick()
	w.tick()
	es, _ := w.Store.Recent(time.Hour)
	if len(es) != 2 || es[1].App != "kitty" {
		t.Fatalf("切窗口应该记两条：%+v", es)
	}
}

// 关窗检测：按 class|pid 对比
func TestClosed(t *testing.T) {
	prev := []Win{{Class: "a", PID: 1}, {Class: "b", PID: 2}}
	cur := []Win{{Class: "b", PID: 2}}
	gone := Closed(prev, cur)
	if len(gone) != 1 || gone[0].Class != "a" {
		t.Fatalf("Closed = %+v", gone)
	}
}

// 配额：到上限就拒绝，而且计数跨实例保留
func TestShotQuota(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if ok, _, err := st.TakeShot(3); err != nil || !ok {
			t.Fatalf("第 %d 次应该允许（err=%v）", i+1, err)
		}
	}
	ok, used, _ := st.TakeShot(3)
	if ok || used != 3 {
		t.Fatalf("超配额应该拒绝：ok=%v used=%d", ok, used)
	}
	st.Close()
	// 重开：计数还在
	st2, _ := Open(filepath.Join(dir, "w.db"))
	defer st2.Close()
	if n := st2.ShotsToday(); n != 3 {
		t.Fatalf("配额没持久化：%d", n)
	}
}

func TestSummary(t *testing.T) {
	es := []Event{
		{At: time.Date(2026, 11, 9, 20, 3, 0, 0, time.Local), Kind: KindSwitch, App: "firefox", Title: "文档"},
		{At: time.Date(2026, 11, 9, 20, 41, 0, 0, time.Local), Kind: KindClose, App: "game", Title: "某某"},
	}
	out := Summary(es)
	if !strings.Contains(out, "20:03 切到 firefox") || !strings.Contains(out, "20:41 关掉 game") {
		t.Errorf("Summary = %q", out)
	}
	if Summary(nil) != "" {
		t.Error("空事件应该返回空串")
	}
}
