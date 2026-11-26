package dream

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xaihi/internal/config"
	"xaihi/internal/llm"
	"xaihi/internal/memory"
	"xaihi/internal/store"
	"xaihi/internal/wiki"
)

func runnerFixture(t *testing.T) (*store.SQLiteStore, string) {
	t.Helper()
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	if err := config.SavePersona("xaihi", "人格"); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(config.Profile{
		Name: "test-dreamer", Model: "fake", BaseURL: "http://dreamer.invalid",
		ContextWindow: 8192, Input: []string{"text"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.SavePersonaProfile(config.PersonaProfile{Name: "xaihi", Memory: true, Wiki: true, Dreamer: "test-dreamer"}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewSQLite(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := &store.Conversation{Title: "工作", Persona: "xaihi"}
	work.Messages = []store.Message{{Role: "user", Content: "喜欢短回答", CreatedAt: time.Now(), Status: "done"}}
	if err := st.Save(work); err != nil {
		t.Fatal(err)
	}
	life := &store.Conversation{Title: "生活", Persona: "life"}
	life.Messages = []store.Message{{Role: "user", Content: "生活秘密", CreatedAt: time.Now(), Status: "done"}}
	if err := st.Save(life); err != nil {
		t.Fatal(err)
	}
	return st, work.ID
}

func TestRunnerDryRunApplyAndIdempotency(t *testing.T) {
	st, convID := runnerFixture(t)
	memStore := memory.New(memory.OwnerRoot(config.DataDir(), "xaihi"))
	if _, err := memStore.SaveWithBasis("global", "偏好", "conv:1#1", memory.BasisExplicit, "原有记忆"); err != nil {
		t.Fatal(err)
	}
	plan := Plan{
		Persona: "xaihi", Summary: "建立偏好索引",
		WikiActions:   []WikiAction{{Action: "write", Name: "preferences", Body: "用户偏好短回答", Evidence: []string{"conv:" + convID + "#1"}}},
		MemoryActions: []MemoryAction{{Action: "create", Body: "只生成计划", Evidence: []string{"conv:" + convID + "#1"}}},
	}
	calls := 0
	chat := ChatFunc(func(ctx context.Context, req llm.ChatRequest) (string, error) {
		calls++
		if req.Tools != nil || req.Model != "fake" {
			t.Errorf("dreamer 请求不应带工具或错误模型：%+v", req)
		}
		prompt := req.Messages[1].Content.(string)
		if strings.Contains(prompt, "生活秘密") || strings.Contains(prompt, "不应存在") {
			t.Errorf("prompt 混入其他人格：%s", prompt)
		}
		return rawPlan(t, plan), nil
	})
	now := time.Now()
	runner := Runner{Store: st, Chat: chat, Now: func() time.Time { now = now.Add(time.Nanosecond); return now }}

	dry, err := runner.Run(context.Background(), Options{Persona: "xaihi", ConversationID: convID, MaxTokens: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Applied || dry.Changed != 0 || dry.AuditPath != "" {
		t.Errorf("dry-run 状态不对：%+v", dry)
	}
	if _, err := wiki.New(wiki.OwnerRoot(config.DataDir(), "xaihi")).Read("preferences"); err == nil {
		t.Error("dry-run 写了 wiki")
	}
	if entries, _ := memStore.List(); len(entries) != 1 {
		t.Errorf("dry-run 写了 memory：%+v", entries)
	}
	if _, err := os.Stat(filepath.Join(config.DataDir(), "dreams")); !os.IsNotExist(err) {
		t.Errorf("dry-run 写了审计目录：%v", err)
	}
	if text := Render(dry); !strings.Contains(text, "仅计划，未应用") {
		t.Errorf("CLI 输出未标明 memory 不应用：%s", text)
	}

	applied, err := runner.Run(context.Background(), Options{Persona: "xaihi", ConversationID: convID, MaxTokens: 2000, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.Changed != 1 || applied.AuditPath == "" {
		t.Fatalf("apply 状态不对：%+v", applied)
	}
	page, err := wiki.New(wiki.OwnerRoot(config.DataDir(), "xaihi")).Read("preferences")
	if err != nil || page.Body != "用户偏好短回答" {
		t.Errorf("wiki 未正确应用：%+v, %v", page, err)
	}
	if entries, _ := memStore.List(); len(entries) != 1 {
		t.Errorf("memory_actions 不应应用：%+v", entries)
	}
	audit, err := os.ReadFile(applied.AuditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit), "喜欢短回答") {
		t.Errorf("审计不应保存完整转录：%s", audit)
	}
	var record AuditRecord
	if err := json.Unmarshal(audit, &record); err != nil || record.Changed != 1 || len(record.Plan.MemoryActions) != 1 {
		t.Errorf("审计结构不对：%s, %v", audit, err)
	}

	// 同一个模型计划再次应用：内容未变应为 noop，不产生 revision 冲突或重复页面。
	again, err := runner.Run(context.Background(), Options{Persona: "xaihi", ConversationID: convID, MaxTokens: 2000, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed != 0 || len(again.Diff) != 1 || again.Diff[0].Status != "noop" {
		t.Errorf("重跑应幂等：%+v", again)
	}
	if calls != 3 {
		t.Errorf("应调用 dreamer 三次，实际 %d", calls)
	}
}

func TestRunnerInvalidOutputWritesNothing(t *testing.T) {
	st, convID := runnerFixture(t)
	runner := Runner{Store: st, Chat: ChatFunc(func(context.Context, llm.ChatRequest) (string, error) {
		return "not json", nil
	})}
	if _, err := runner.Run(context.Background(), Options{Persona: "xaihi", ConversationID: convID, Apply: true}); err == nil {
		t.Fatal("非法模型输出应该失败")
	}
	pages, _ := wiki.New(wiki.OwnerRoot(config.DataDir(), "xaihi")).List()
	if len(pages) != 0 {
		t.Errorf("非法输出写了 wiki：%+v", pages)
	}
	if _, err := os.Stat(filepath.Join(config.DataDir(), "dreams")); !os.IsNotExist(err) {
		t.Errorf("非法输出写了审计：%v", err)
	}
}

func TestRunnerMissingDreamerFailsBeforeModelCall(t *testing.T) {
	st, convID := runnerFixture(t)
	if err := config.DeleteProfile("test-dreamer"); err != nil {
		t.Fatal(err)
	}
	called := false
	runner := Runner{Store: st, Chat: ChatFunc(func(context.Context, llm.ChatRequest) (string, error) {
		called = true
		return "", nil
	})}
	if _, err := runner.Run(context.Background(), Options{Persona: "xaihi", ConversationID: convID, Apply: true}); err == nil || !strings.Contains(err.Error(), "不存在的 profile") {
		t.Fatalf("失效 dreamer 应明确报错：%v", err)
	}
	if called {
		t.Error("dreamer 配置失效时不应调用模型")
	}
}
