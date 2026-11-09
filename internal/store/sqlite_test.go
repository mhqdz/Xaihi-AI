package store

import (
	"testing"

	"xaihi/internal/llm"
)

func newSQ(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLite(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func TestSQLiteCreateAndAppend(t *testing.T) {
	s := newSQ(t)
	c := &Conversation{Title: "新会话"}
	if err := s.Save(c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if c.ID == "" || c.ID == "0" {
		t.Fatalf("期望自增 id，得到 %q", c.ID)
	}

	c.Messages = append(c.Messages, Message{Role: "user", Content: "第一句"})
	if err := s.Save(c); err != nil {
		t.Fatalf("save2: %v", err)
	}
	c.Messages = append(c.Messages, Message{Role: "assistant", Content: "回复"})
	if err := s.Save(c); err != nil {
		t.Fatalf("save3: %v", err)
	}

	got, err := s.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("期望 2 条消息（不重复插入），得到 %d", len(got.Messages))
	}
	if got.Messages[0].Seq == 0 || got.Messages[0].Seq >= got.Messages[1].Seq {
		t.Fatalf("seq 应为递增正整数，得到 %d, %d", got.Messages[0].Seq, got.Messages[1].Seq)
	}
	if got.Messages[1].Content != "回复" {
		t.Fatalf("内容丢了: %q", got.Messages[1].Content)
	}
}

func TestSQLiteToolCallsRoundTrip(t *testing.T) {
	s := newSQ(t)
	c := &Conversation{Title: "t"}
	_ = s.Save(c)
	c.Messages = append(c.Messages,
		Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function",
			Function: llm.FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}}}},
		Message{Role: "tool", ToolCallID: "call_1", ToolResult: "[exit 0] ok"},
	)
	if err := s.Save(c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := s.Get(c.ID)
	if len(got.Messages) != 2 || len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("tool_calls 未正确往返: %+v", got.Messages)
	}
	if got.Messages[0].ToolCalls[0].Function.Name != "bash" {
		t.Fatalf("函数名丢了: %+v", got.Messages[0].ToolCalls[0])
	}
	if got.Messages[1].ToolResult == nil {
		t.Fatalf("tool_result 丢了")
	}
}

func TestSQLiteArchiveRestoreList(t *testing.T) {
	s := newSQ(t)
	a := &Conversation{Title: "a"}
	b := &Conversation{Title: "b"}
	_ = s.Save(a)
	_ = s.Save(b)

	if err := s.Archive(a.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	live, _ := s.List()
	if len(live) != 1 || live[0].ID != b.ID {
		t.Fatalf("活跃列表不对: %+v", live)
	}
	arch, _ := s.ListArchived()
	if len(arch) != 1 || arch[0].ID != a.ID {
		t.Fatalf("归档列表不对: %+v", arch)
	}
	if _, err := s.Get(a.ID); err == nil {
		t.Fatalf("归档会话不应出现在 Get（当前实现限定 archived=0）")
	}
	if err := s.Restore(a.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := s.Get(a.ID); err != nil {
		t.Fatalf("恢复后应可读: %v", err)
	}
}

func TestSQLiteNextIDsUnique(t *testing.T) {
	s := newSQ(t)
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		c := &Conversation{Title: "x"}
		if err := s.Save(c); err != nil {
			t.Fatalf("save: %v", err)
		}
		if seen[c.ID] {
			t.Fatalf("重复 id: %s", c.ID)
		}
		seen[c.ID] = true
	}
}
