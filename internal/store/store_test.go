package store

import (
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *JSONStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSeqIsMonotonicAndStable(t *testing.T) {
	s := newStore(t)
	c := &Conversation{ID: "abc", Title: "t"}
	c.Messages = append(c.Messages, Message{Role: "user", Content: "a"})
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	if c.Messages[0].Seq != 1 {
		t.Fatalf("第一条应为 1，得到 %d", c.Messages[0].Seq)
	}
	c.Messages = append(c.Messages, Message{Role: "assistant", Content: "b"})
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	if c.Messages[1].Seq != 2 {
		t.Fatalf("第二条应为 2，得到 %d", c.Messages[1].Seq)
	}
	// 再存一次不应该改变已有序号
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages[0].Seq != 1 || got.Messages[1].Seq != 2 {
		t.Fatalf("序号被改动: %+v", got.Messages)
	}
}

func TestLegacyFileGetsSeqOnLoad(t *testing.T) {
	s := newStore(t)
	// 手写一个没有 seq 的老文件
	raw := `{"id":"old","title":"老会话","messages":[{"role":"user","content":"x"},{"role":"assistant","content":"y"}]}`
	if err := os.WriteFile(filepath.Join(s.liveDir(), "conv-old.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := s.Get("old")
	if err != nil {
		t.Fatal(err)
	}
	if c.Messages[0].Seq != 1 || c.Messages[1].Seq != 2 {
		t.Fatalf("老数据补序号失败: %+v", c.Messages)
	}
}

func TestArchiveRestoreDelete(t *testing.T) {
	s := newStore(t)
	c := &Conversation{ID: "x1", Title: "归档测试"}
	c.Messages = append(c.Messages, Message{Role: "user", Content: "hi"})
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Archive("x1"); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(); len(l) != 0 {
		t.Fatalf("归档后活跃列表应为空，得到 %d", len(l))
	}
	if a, _ := s.ListArchived(); len(a) != 1 || a[0].ID != "x1" {
		t.Fatalf("归档列表不对: %+v", a)
	}
	if err := s.Restore("x1"); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(); len(l) != 1 {
		t.Fatalf("恢复后活跃列表应为 1，得到 %d", len(l))
	}
	if err := s.Delete("x1", false); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.ListArchived(); len(a) != 1 {
		t.Fatalf("软删应进归档: %+v", a)
	}
	if err := s.Restore("x1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("x1", true); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(); len(l) != 0 {
		t.Fatalf("硬删后应为空")
	}
	if a, _ := s.ListArchived(); len(a) != 0 {
		t.Fatalf("硬删后归档也应为空")
	}
}

func TestGetMissing(t *testing.T) {
	s := newStore(t)
	if _, err := s.Get("nope"); err == nil {
		t.Fatal("取不存在的会话应报错")
	}
}
