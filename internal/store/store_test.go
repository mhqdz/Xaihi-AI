package store

import (
	"fmt"
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

// KV 是队列：超上限丢最旧的，顺序稳定
func TestPutKVQueue(t *testing.T) {
	c := &Conversation{}
	for i := 0; i < MaxKV+3; i++ {
		PutKV(c, fmt.Sprintf("k%02d", i), fmt.Sprint(i))
	}
	if len(c.KV) != MaxKV || len(c.KVSeq) != MaxKV {
		t.Fatalf("应该压到 %d 条，得到 %d/%d", MaxKV, len(c.KV), len(c.KVSeq))
	}
	// 最旧的三条应该被淘汰
	for _, k := range []string{"k00", "k01", "k02"} {
		if _, ok := c.KV[k]; ok {
			t.Errorf("%s 应该被淘汰", k)
		}
	}
	if c.KVSeq[0] != "k03" {
		t.Errorf("队首应该是 k03，得到 %s", c.KVSeq[0])
	}
	// 覆盖已有 key 不改变顺序
	PutKV(c, "k03", "改了")
	if c.KVSeq[0] != "k03" || c.KV["k03"] != "改了" {
		t.Errorf("覆盖不该动顺序：%v %v", c.KVSeq[0], c.KV["k03"])
	}
}

// 注入顺序：有 KVSeq 按插入顺序，老数据按字典序补齐
func TestKVPairsOrder(t *testing.T) {
	c := &Conversation{KV: map[string]string{"b": "2", "a": "1", "c": "3"}}
	got := c.KVPairs()
	if len(got) != 3 || got[0][0] != "a" || got[2][0] != "c" {
		t.Fatalf("老数据应按字典序：%v", got)
	}
	c2 := &Conversation{KV: map[string]string{"z": "9", "a": "1"}, KVSeq: []string{"z", "a"}}
	if got := c2.KVPairs(); got[0][0] != "z" {
		t.Fatalf("应按 KVSeq：%v", got)
	}
}
