package dream

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"xaihi/internal/store"
)

func transcriptFixture(t *testing.T) (*store.SQLiteStore, string, string) {
	t.Helper()
	st, err := store.NewSQLite(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	work := &store.Conversation{Title: "工作", Persona: "work", CreatedAt: now, UpdatedAt: now}
	work.Messages = []store.Message{
		{Role: "user", Content: "用户事实", CreatedAt: now, Status: "done"},
		{Role: "assistant", Content: "助手正文", Reasoning: "不应进入的推理", CreatedAt: now, Status: "done"},
		{Role: "tool", Content: strings.Repeat("长", 1000), CreatedAt: now, Status: "done"},
		{Role: "assistant", Content: "未完成", CreatedAt: now, Status: "running"},
		{Role: "user", Content: "保留文字", Images: []string{"data:image/png;base64,SECRET"}, CreatedAt: now, Status: "done"},
	}
	if err := st.Save(work); err != nil {
		t.Fatal(err)
	}
	life := &store.Conversation{Title: "生活", Persona: "life", CreatedAt: now, UpdatedAt: now}
	life.Messages = []store.Message{{Role: "user", Content: "生活秘密", CreatedAt: now, Status: "done"}}
	if err := st.Save(life); err != nil {
		t.Fatal(err)
	}
	return st, work.ID, life.ID
}

func TestReadTranscriptsFiltersPersonaAndContent(t *testing.T) {
	st, workID, lifeID := transcriptFixture(t)
	corpus, err := ReadTranscripts(context.Background(), st, Options{Persona: "work", ConversationID: workID, MaxTokens: 3000})
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus.Messages) != 4 {
		t.Fatalf("应保留 user/assistant/tool/含图文字四条，得到 %+v", corpus.Messages)
	}
	for _, m := range corpus.Messages {
		joined := m.Text + m.Kind
		if strings.Contains(joined, "推理") || strings.Contains(joined, "SECRET") || strings.Contains(joined, "未完成") {
			t.Errorf("过滤失败：%+v", m)
		}
		if m.Role == "tool" && (m.Kind != "tool_observation" || len([]rune(m.Text)) > toolResultRunes+10) {
			t.Errorf("工具结果未截断：%+v", m)
		}
	}
	if len(corpus.Ranges) != 1 || corpus.Ranges[0].ConvID != workID {
		t.Errorf("输入范围不对：%+v", corpus.Ranges)
	}
	if !corpus.HasEvidence("conv:" + workID + "#1") {
		t.Errorf("证据引用不可校验：%+v", corpus.Messages)
	}
	if _, err := ReadTranscripts(context.Background(), st, Options{Persona: "work", ConversationID: lifeID}); !errors.Is(err, ErrCrossPersona) {
		t.Errorf("指定其他人格会话应明确拒绝：%v", err)
	}
	if _, err := ReadTranscripts(context.Background(), st, Options{Persona: "work", Since: time.Now()}); err != ErrNoRange {
		// Since 非零是合法范围，这里只确保没有意外当成 ErrNoRange；实际应无结果。
		if err == nil {
			t.Error("空结果应明确报错")
		}
	} else {
		t.Errorf("有 since 不应返回 ErrNoRange：%v", err)
	}
}

func TestReadTranscriptsRequiresRangeAndBudget(t *testing.T) {
	st, workID, _ := transcriptFixture(t)
	if _, err := ReadTranscripts(context.Background(), st, Options{Persona: "work", MaxTokens: 1000}); !errors.Is(err, ErrNoRange) {
		t.Errorf("未指定范围应返回 ErrNoRange：%v", err)
	}
	corpus, err := ReadTranscripts(context.Background(), st, Options{Persona: "work", ConversationID: workID, MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !corpus.Truncated || len(corpus.Messages) >= 4 || corpus.Tokens > 100 {
		t.Errorf("预算没有生效：tokens=%d messages=%d truncated=%v", corpus.Tokens, len(corpus.Messages), corpus.Truncated)
	}
	if _, err := ReadTranscripts(context.Background(), st, Options{Persona: "", ConversationID: workID}); err == nil {
		t.Error("空 persona 不应读取任何会话")
	}
}
