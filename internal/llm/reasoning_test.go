package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// 自适应：第一次收到哪种字段就用哪种回传；一次都没收到就不回传。
func TestLearnReasoningField(t *testing.T) {
	c := New("http://x", "")
	if c.ReasoningField() != "" {
		t.Fatal("默认应该是空（不吐思考的上游）")
	}
	c.LearnReasoningField("reasoning_content")
	if got := c.ReasoningField(); got != "reasoning_content" {
		t.Fatalf("ReasoningField = %q", got)
	}
	// 只记第一次：之后再来别的名字不改
	c.LearnReasoningField("reasoning")
	if got := c.ReasoningField(); got != "reasoning_content" {
		t.Errorf("不该被覆盖: %q", got)
	}
	// 空值忽略
	c2 := New("http://x", "")
	c2.LearnReasoningField("")
	if c2.ReasoningField() != "" {
		t.Error("空字段名不该被记住")
	}
}

// Message 的 JSON：只会出现一个思考字段
func TestMessageReasoningJSON(t *testing.T) {
	m := Message{Role: "assistant", Content: "hi", ReasoningContent: "想了"}
	b, _ := jsonMarshal(m)
	if !contains(string(b), `"reasoning_content":"想了"`) {
		t.Errorf("json = %s", b)
	}
	if contains(string(b), `"reasoning":`) {
		t.Errorf("不该同时出现 reasoning: %s", b)
	}
	m2 := Message{Role: "assistant", Content: "hi", Reasoning: "想了"}
	b2, _ := jsonMarshal(m2)
	if !contains(string(b2), `"reasoning":"想了"`) || contains(string(b2), `reasoning_content`) {
		t.Errorf("json = %s", b2)
	}
	// 两个都空：都不出现
	b3, _ := jsonMarshal(Message{Role: "user", Content: "x"})
	if contains(string(b3), "reasoning") {
		t.Errorf("空值不该出现: %s", b3)
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
func contains(s, sub string) bool       { return strings.Contains(s, sub) }
