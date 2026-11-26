package dream

import (
	"encoding/json"
	"strings"
	"testing"

	"xaihi/internal/wiki"
)

func planCorpus() Corpus {
	return Corpus{
		Persona: "xaihi",
		Messages: []SourceMessage{
			{ConvID: "1", Seq: 1, Role: "user", Text: "原文"},
			{ConvID: "1", Seq: 2, Role: "tool", Text: "观察"},
		},
		Ranges: []InputRange{{ConvID: "1", FromSeq: 1, ToSeq: 2}},
	}
}

func rawPlan(t *testing.T, p Plan) string {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func validPlan() Plan {
	return Plan{
		Persona: "xaihi", Summary: "测试",
		WikiActions:   []WikiAction{{Action: "write", Name: "index", Body: "# 索引", KBLinks: []string{"0100", "0004"}, Evidence: []string{"conv:1#1"}}},
		MemoryActions: []MemoryAction{{Action: "create", Body: "值得记", Evidence: []string{"conv:1#2"}}},
	}
}

func TestParsePlanValidatesAndDefaults(t *testing.T) {
	plan, diffs, err := ParsePlan("```json\n"+rawPlan(t, validPlan())+"\n```", planCorpus(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 || diffs[0].Status != "write" || len(plan.Inputs) != 1 {
		t.Fatalf("解析或 diff 不对：%+v %+v", plan, diffs)
	}
	if strings.Join(plan.WikiActions[0].KBLinks, ",") != "0004,0100" {
		t.Errorf("kb links 未规范化：%+v", plan.WikiActions[0].KBLinks)
	}
	m := plan.MemoryActions[0]
	if m.Basis != "inferred" || m.Kind != "记录" || m.Source != "conv:1#2" {
		t.Errorf("memory 默认值不对：%+v", m)
	}
}

func TestParsePlanRejectsBadEvidenceAndVersions(t *testing.T) {
	for name, mutate := range map[string]func(*Plan){
		"persona":  func(p *Plan) { p.Persona = "other" },
		"evidence": func(p *Plan) { p.WikiActions[0].Evidence = []string{"conv:999#1"} },
		"action":   func(p *Plan) { p.WikiActions[0].Action = "replace" },
		"name":     func(p *Plan) { p.WikiActions[0].Name = "../bad" },
		"kb":       func(p *Plan) { p.WikiActions[0].KBLinks = []string{"../kb"} },
	} {
		p := validPlan()
		mutate(&p)
		if _, _, err := ParsePlan(rawPlan(t, p), planCorpus(), nil); err == nil {
			t.Errorf("%s 应该被拒绝", name)
		}
	}
	if _, _, err := ParsePlan("not json", planCorpus(), nil); err == nil {
		t.Error("非法 JSON 应该被拒绝")
	}
	p := validPlan()
	raw := rawPlan(t, p) + rawPlan(t, p)
	if _, _, err := ParsePlan(raw, planCorpus(), nil); err == nil {
		t.Error("多个 JSON 对象应该被拒绝")
	}
}

func TestParsePlanRevisionAndIdempotentNoop(t *testing.T) {
	store := wiki.New(t.TempDir())
	old, err := store.Write("index", "旧正文", []string{"0004"}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := validPlan()
	p.WikiActions[0].Body = "新正文"
	p.WikiActions[0].Revision = "stale"
	if _, _, err := ParsePlan(rawPlan(t, p), planCorpus(), []wiki.Entry{old}); err == nil {
		t.Fatal("过期 revision 应该被拒绝")
	}
	p.WikiActions[0].Revision = old.Revision
	_, diffs, err := ParsePlan(rawPlan(t, p), planCorpus(), []wiki.Entry{old})
	if err != nil || len(diffs) != 1 || diffs[0].Status != "write" {
		t.Fatalf("正确 revision 应允许更新：%+v, %v", diffs, err)
	}

	// 内容已经等于目标时，即使带旧 revision 也归为 noop；删除不存在页面同样幂等。
	p.WikiActions[0].Body = old.Body
	p.WikiActions[0].KBLinks = old.KBLinks
	p.WikiActions[0].Revision = "old"
	_, diffs, err = ParsePlan(rawPlan(t, p), planCorpus(), []wiki.Entry{old})
	if err != nil || diffs[0].Status != "noop" {
		t.Errorf("重复写应为 noop：%+v, %v", diffs, err)
	}
	p.WikiActions[0].Action = "delete"
	p.WikiActions[0].Name = "missing"
	_, diffs, err = ParsePlan(rawPlan(t, p), planCorpus(), []wiki.Entry{old})
	if err != nil || diffs[0].Status != "noop" {
		t.Errorf("重复删除应为 noop：%+v, %v", diffs, err)
	}
}
