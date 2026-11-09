package handoff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"xaihi/internal/store"
)

func newMgr(t *testing.T) (*Manager, *store.SQLiteStore) {
	t.Helper()
	st, err := store.NewSQLite(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(st, t.TempDir(), Hooks{
		RunChild: func(ctx context.Context, parent int64, profile, card string, maxRounds int) (int64, string, error) {
			return 42, "子会话完成", nil
		},
		RunTurn: func(ctx context.Context, conv int64, text, origin string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, st
}

func TestRecipeLoadAndDispatch(t *testing.T) {
	m, st := newMgr(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "echo.json"), []byte(`{
	  "kind":"echo-test","profile":"9-exec","max_rounds":5,"timeout_s":30,
	  "steps":[{"run":"echo hello"},{"spawn":true}],
	  "restore":[{"run":"true"}]}`), 0o644)
	if err := m.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if len(m.Names()) != 1 || m.Names()[0] != "echo-test" {
		t.Fatalf("配方没加载: %v", m.Names())
	}
	c := &store.Conversation{Title: "p"}
	if err := st.Save(c); err != nil {
		t.Fatal(err)
	}
	parent := int64(1)
	if _, err := st.CreateTask("x", parent, ""); err != nil {
		t.Fatal(err)
	}
	tid, err := m.Dispatch(parent, "echo-test", map[string]any{"goal": "试一下"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		task, err := st.GetTask(tid)
		if err == nil && (task.Status == "done" || task.Status == "failed") {
			if task.Status != "done" {
				t.Fatalf("任务状态 %s: %s", task.Status, task.Error)
			}
			steps, _ := st.Steps(tid)
			if len(steps) < 3 {
				t.Fatalf("步骤太少: %d", len(steps))
			}
			if task.ChildConv != 42 {
				t.Fatalf("子会话 id 没记录: %d", task.ChildConv)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("任务超时未结束")
}

func TestUnknownKind(t *testing.T) {
	m, _ := newMgr(t)
	if _, err := m.Dispatch(1, "nope", nil); err == nil {
		t.Fatal("未注册的 kind 应该报错")
	}
}

func TestOutboxFIFO(t *testing.T) {
	m, st := newMgr(t)
	var got []string
	m.hooks.RunTurn = func(ctx context.Context, conv int64, text, origin string) error {
		got = append(got, text)
		return nil
	}
	for _, s := range []string{"a", "b", "c"} {
		if _, err := st.CreateOutbox(1, "system", s); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ConsumeOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("FIFO 顺序不对: %v", got)
	}
	left, _ := st.PendingOutbox()
	if len(left) != 0 {
		t.Fatalf("还有 pending: %d", len(left))
	}
}
