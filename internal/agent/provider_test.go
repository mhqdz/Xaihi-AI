package agent

import (
	"testing"

	"xaihi/internal/config"
)

func TestProviderReasoningParams(t *testing.T) {
	a := New(config.Config{}, nil, config.Profile{Name: "x", Model: "m", ReasoningEffort: "high"})
	if a.reasoningBudgetParam() == 0 {
		t.Fatal("本地 provider 应该发 reasoning_budget_tokens")
	}
	if a.reasoningEffort() != "high" {
		t.Fatal("本地 provider 应该保留 effort")
	}
	b := New(config.Config{}, nil, config.Profile{Name: "y", Model: "m", Provider: "openai", ReasoningEffort: "high"})
	if b.reasoningBudgetParam() != 0 {
		t.Fatal("openai 兼容云端不应发 reasoning_budget_tokens")
	}
	if b.reasoningEffort() != "high" {
		t.Fatal("openai 兼容仍可发标准 effort（high）")
	}
	c := New(config.Config{}, nil, config.Profile{Name: "z", Model: "m", Provider: "openai", ReasoningEffort: "none"})
	if c.reasoningEffort() != "" {
		t.Fatal("effort=none 在云端应省略")
	}
}
