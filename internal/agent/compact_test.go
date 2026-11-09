package agent

import (
	"strings"
	"testing"

	"xaihi/internal/llm"
)

func mk(n int) []llm.Message {
	// 交替 user/assistant，带 seq 不重要，这里只测压缩函数
	out := []llm.Message{{Role: "system", Content: "sys"}}
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		out = append(out, llm.Message{Role: role, Content: strings.Repeat("x", 100)})
	}
	return out
}

func TestCompactNoopWhenSmall(t *testing.T) {
	in := mk(4) // 4 条 < keep 16
	out := compact(in, 16, 1000000)
	if len(out) != len(in) {
		t.Fatalf("小上下文不应压缩: %d → %d", len(in), len(out))
	}
}

func TestCompactKeepsRecentAndSystem(t *testing.T) {
	in := mk(40)
	out := compact(in, 10, 1) // budget=1 强制压缩
	if out[0].Role != "system" || out[0].Content.(string) != "sys" {
		t.Fatalf("system 应保留: %+v", out[0])
	}
	if !strings.Contains(out[1].Content.(string), "上下文压缩") {
		t.Fatalf("应有省略提示: %v", out[1].Content)
	}
	if len(out) > 41 {
		t.Fatalf("不应超过原长度: %d", len(out))
	}
	// 保留的尾部要与原尾部一致
	if out[len(out)-1].Content.(string) != in[len(in)-1].Content.(string) {
		t.Fatal("尾部消息应原样保留")
	}
}

func TestCompactBoundaryIsUser(t *testing.T) {
	in := mk(30)
	out := compact(in, 5, 3) // 预算很小，强制压缩
	// 省略提示之后的第一条应当是 user（否则会把 tool_call 与结果拆开）
	if len(out) > 2 && out[2].Role != "user" {
		t.Fatalf("切点应落在 user 上，得到 %q", out[2].Role)
	}
}

func TestCompactKeepsMoreWhenBudgetAllows(t *testing.T) {
	in := mk(40) // 40*100 = 4000 字符
	out := compact(in, 10, 3000)
	// 4000 < 3000/2? 否 → 会压缩，但应保留"预算的一半"这一大段，而不是固定 10 条
	if len(out) <= 12 {
		t.Fatalf("按预算应保留更多，实际 %d 条", len(out))
	}
}
