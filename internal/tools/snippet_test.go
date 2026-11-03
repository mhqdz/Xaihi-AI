package tools

import "testing"

func TestSnippetCJKOffset(t *testing.T) {
	// 前面是长中文前缀（每字 3 字节），关键词在中段。
	// 用字节偏移去切 rune 切片会导致摘录错位或永远退化成「前 120 字」。
	prefix := ""
	for i := 0; i < 60; i++ {
		prefix += "前缀"
	}
	body := prefix + "关键词出现在这里" + prefix + "结尾"
	got := snippet(body, "关键词", 20)
	if got == truncate(body, 120) {
		t.Fatalf("摘录退化成了前段，说明仍在按字节索引切 rune：%q", got)
	}
	if !containsFold(got, "关键词") {
		t.Errorf("摘录没包含关键词：%q", got)
	}
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
