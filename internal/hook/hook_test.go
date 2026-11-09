package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeHook(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newManager(t *testing.T) (*Manager, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	m := New(t.TempDir(), func(ctx context.Context, d Def, text string) error {
		calls.Add(1)
		return nil
	})
	return m, calls
}

// 触发是异步的：等它真的跑起来（有上限，避免测试挂死）
func waitCalls(t *testing.T, calls *atomic.Int32, want int32) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if calls.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等不到 %d 次调用（当前 %d）", want, calls.Load())
}

func TestLoadAndTrigger(t *testing.T) {
	m, calls := newManager(t)
	writeHook(t, m.Dir, "game", `{"name":"game","mode":"speak","prompt":"我从游戏出来了"}`)
	if warns := m.Load(); len(warns) != 0 {
		t.Fatalf("警告: %v", warns)
	}
	if got := m.Names(); len(got) != 1 || got[0] != "game" {
		t.Fatalf("Names = %v", got)
	}
	msg, err := m.Trigger(context.Background(), "game", map[string]any{"app": "steam"})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	waitCalls(t, calls, 1)
	if !strings.Contains(msg, "1/10") {
		t.Errorf("配额提示不对: %s", msg)
	}
}

// 冷却窗口内第二次要被拒
func TestCooldown(t *testing.T) {
	m, calls := newManager(t)
	writeHook(t, m.Dir, "x", `{"name":"x","prompt":"p","cooldown_s":3}`)
	m.Load()
	if _, err := m.Trigger(context.Background(), "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Trigger(context.Background(), "x", nil); err == nil {
		t.Fatal("冷却期内应该被拒")
	}
	time.Sleep(50 * time.Millisecond) // 给异步一次机会（不该有第二次）
	if calls.Load() != 1 {
		t.Fatalf("被拒的那次不该跑，calls=%d", calls.Load())
	}
}

// 配额到顶要拒
func TestQuota(t *testing.T) {
	m, calls := newManager(t)
	writeHook(t, m.Dir, "q", `{"name":"q","prompt":"p","quota_per_day":2,"cooldown_s":1}`)
	m.Load()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := m.Trigger(ctx, "q", nil); err != nil {
			t.Fatalf("第 %d 次应该允许: %v", i+1, err)
		}
		// 绕开冷却：直接把上次时间往前推
		m.mu.Lock()
		m.lastAt["q"] = time.Now().Add(-time.Hour)
		m.mu.Unlock()
	}
	if _, err := m.Trigger(ctx, "q", nil); err == nil || !strings.Contains(err.Error(), "满") {
		t.Fatalf("超配额应该报满，得到 %v", err)
	}
	waitCalls(t, calls, 2)
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatalf("超配额的那次不该跑，calls=%d", calls.Load())
	}
}

func TestUnknownHook(t *testing.T) {
	m, _ := newManager(t)
	if _, err := m.Trigger(context.Background(), "nope", nil); err == nil {
		t.Fatal("不存在的钩子应该报错")
	}
}

// 上下文里必须带触发器名字和数据
func TestBuildText(t *testing.T) {
	d := Def{Name: "game", Prompt: "我从游戏出来了", Mode: ModeSpeak}
	got := BuildText(d, map[string]any{"app": "steam"})
	if !strings.Contains(got, "[触发器 game]") || !strings.Contains(got, `"app":"steam"`) {
		t.Errorf("BuildText = %q", got)
	}
	if !strings.Contains(got, "我从游戏出来了") {
		t.Errorf("缺 prompt: %q", got)
	}
	// 静默触发要明确告诉模型别开口
	if s := BuildText(Def{Name: "s", Prompt: "p", Mode: ModeSilent}, nil); !strings.Contains(s, "静默") {
		t.Errorf("静默模式没提示: %q", s)
	}
}

func TestLoadSkipsBroken(t *testing.T) {
	m, _ := newManager(t)
	writeHook(t, m.Dir, "ok", `{"name":"ok","prompt":"p"}`)
	writeHook(t, m.Dir, "bad", `{not json`)
	writeHook(t, m.Dir, "empty", `{"name":"","prompt":""}`)
	warns := m.Load()
	if len(warns) != 2 {
		t.Errorf("应该报两条警告，得到 %v", warns)
	}
	if got := m.Names(); len(got) != 1 {
		t.Errorf("好的钩子应该还在: %v", got)
	}
}

// 目录变了自动重读（改钩子不用重启）
func TestWatchDirReload(t *testing.T) {
	m, _ := newManager(t)
	m.Load()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.WatchDir(ctx, 20*time.Millisecond)

	writeHook(t, m.Dir, "new", `{"name":"new","prompt":"p"}`)
	for i := 0; i < 200; i++ {
		if m.Has("new") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("热重载没生效")
}

// 注册：校验 + 落盘 + 再读出来是同一个东西
func TestWriteAndLoad(t *testing.T) {
	dir := t.TempDir()
	d := Def{Name: "game-exit", Mode: ModeSilent, Prompt: "我从游戏出来了", QuotaPerDay: 5, CooldownS: 2}
	if err := Write(dir, d); err != nil {
		t.Fatalf("Write: %v", err)
	}
	defs, _ := LoadDir(dir)
	if len(defs) != 1 {
		t.Fatalf("应该读回 1 个，得到 %d", len(defs))
	}
	got := defs[0]
	if got.Name != d.Name || got.Mode != d.Mode || got.Prompt != d.Prompt || got.QuotaPerDay != 5 {
		t.Errorf("读回不一致：%+v", got)
	}
	// 删掉
	if err := Remove(dir, "game-exit"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if defs, _ := LoadDir(dir); len(defs) != 0 {
		t.Errorf("删完还有：%v", defs)
	}
	if err := Remove(dir, "game-exit"); err == nil {
		t.Error("删不存在的钩子应该报错")
	}
}

// 名字即文件名：绝不允许路径分隔符
func TestValidateName(t *testing.T) {
	dir := t.TempDir()
	bad := []string{"../evil", "a/b", "A-B", "", "有中文", strings.Repeat("x", 40)}
	for _, n := range bad {
		if err := Write(dir, Def{Name: n, Prompt: "p"}); err == nil {
			t.Errorf("%q 应该被拒", n)
		}
	}
	if err := Write(dir, Def{Name: "ok-name_1", Prompt: "p"}); err != nil {
		t.Errorf("合法名字被拒: %v", err)
	}
}

func TestValidateFields(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Def{Name: "x", Prompt: "  "}); err == nil {
		t.Error("空 prompt 应该被拒")
	}
	if err := Write(dir, Def{Name: "x", Prompt: "p", Mode: "loud"}); err == nil {
		t.Error("非法 mode 应该被拒")
	}
	if err := Write(dir, Def{Name: "x", Prompt: "p", QuotaPerDay: 999}); err == nil {
		t.Error("超配额上限应该被拒")
	}
	if err := Write(dir, Def{Name: "x", Prompt: "p", CooldownS: -1}); err == nil {
		t.Error("负冷却应该被拒")
	}
}

// conv_id 要能落盘并读回（钩子绑会话）
func TestWriteConvID(t *testing.T) {
	dir := t.TempDir()
	d := Def{Name: "bound", Prompt: "盯着 steam", QuotaPerDay: 3, CooldownS: 2, ConvID: 42}
	if err := Write(dir, d); err != nil {
		t.Fatalf("Write: %v", err)
	}
	defs, _ := LoadDir(dir)
	if len(defs) != 1 || defs[0].ConvID != 42 {
		t.Fatalf("conv_id 没落盘：%+v", defs)
	}
	// 不绑会话时不该写这个字段（保持文件干净）
	d2 := Def{Name: "free", Prompt: "p"}
	if err := Write(dir, d2); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "free.json"))
	if strings.Contains(string(b), "conv_id") {
		t.Errorf("没绑会话却写了 conv_id：%s", b)
	}
}
