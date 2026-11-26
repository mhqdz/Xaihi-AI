package dream

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"xaihi/internal/memory"
	"xaihi/internal/wiki"
)

var (
	reWikiName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	reKBID     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// ParsePlan 严格解析模型输出，并在任何写入前校验证据、页面版本和动作上限。
func ParsePlan(raw string, corpus Corpus, existing []wiki.Entry) (*Plan, []DiffEntry, error) {
	if len(raw) > 4<<20 {
		return nil, nil, fmt.Errorf("dreaming 输出超过 4MB")
	}
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		if i := strings.Index(text, "\n"); i >= 0 {
			text = text[i+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, nil, fmt.Errorf("dreaming 没有输出 JSON 对象")
	}
	dec := json.NewDecoder(strings.NewReader(text[start : end+1]))
	dec.DisallowUnknownFields()
	var plan Plan
	if err := dec.Decode(&plan); err != nil {
		return nil, nil, fmt.Errorf("解析 DreamPlan：%w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, nil, fmt.Errorf("DreamPlan 只能包含一个 JSON 对象")
	}
	if plan.Persona != corpus.Persona {
		return nil, nil, fmt.Errorf("DreamPlan persona=%q 与输入 %q 不一致", plan.Persona, corpus.Persona)
	}
	plan.Inputs = append([]InputRange(nil), corpus.Ranges...)
	if len(plan.WikiActions) > 100 || len(plan.MemoryActions) > 100 {
		return nil, nil, fmt.Errorf("单次 dreaming 每类动作最多 100 个")
	}
	pages := make(map[string]wiki.Entry, len(existing))
	for _, e := range existing {
		pages[e.Name] = e
	}
	seenPages := map[string]bool{}
	var diffs []DiffEntry
	for i := range plan.WikiActions {
		a := &plan.WikiActions[i]
		if seenPages[a.Name] {
			return nil, nil, fmt.Errorf("同一 wiki 页面出现重复动作：%s", a.Name)
		}
		seenPages[a.Name] = true
		if !reWikiName.MatchString(a.Name) {
			return nil, nil, fmt.Errorf("非法 wiki 页面名：%q", a.Name)
		}
		if err := validateEvidence(a.Evidence, corpus); err != nil {
			return nil, nil, fmt.Errorf("wiki %s：%w", a.Name, err)
		}
		links, err := normalizeKBLinks(a.KBLinks)
		if err != nil {
			return nil, nil, err
		}
		a.KBLinks = links
		old, exists := pages[a.Name]
		d := DiffEntry{Action: a.Action, Name: a.Name, OldBody: old.Body, NewBody: a.Body}
		switch a.Action {
		case "write":
			if strings.TrimSpace(a.Body) == "" || len([]rune(a.Body)) > wiki.MaxBodyRunes {
				return nil, nil, fmt.Errorf("wiki %s 正文为空或超过 %d 字", a.Name, wiki.MaxBodyRunes)
			}
			if exists && strings.TrimSpace(old.Body) == strings.TrimSpace(a.Body) && equalStrings(old.KBLinks, links) {
				d.Status = "noop" // 重跑同一计划时，即使 revision 已旧也不重复写
			} else if exists {
				if a.Revision == "" || a.Revision != old.Revision {
					return nil, nil, fmt.Errorf("wiki %s revision 冲突，需要重新读取", a.Name)
				}
				d.Status = "write"
			} else {
				if a.Revision != "" {
					return nil, nil, fmt.Errorf("wiki %s 不存在，不能按旧 revision 更新", a.Name)
				}
				d.Status = "write"
			}
		case "delete":
			d.NewBody = ""
			if !exists {
				d.Status = "noop" // 上次已经删除，重跑保持幂等
			} else {
				if a.Revision == "" || a.Revision != old.Revision {
					return nil, nil, fmt.Errorf("wiki %s 删除 revision 冲突", a.Name)
				}
				d.Status = "delete"
			}
		default:
			return nil, nil, fmt.Errorf("wiki %s 未知动作 %q（只允许 write/delete）", a.Name, a.Action)
		}
		diffs = append(diffs, d)
	}
	for i := range plan.MemoryActions {
		a := &plan.MemoryActions[i]
		if err := validateEvidence(a.Evidence, corpus); err != nil {
			return nil, nil, fmt.Errorf("memory %s：%w", a.Name, err)
		}
		if a.Source == "" {
			a.Source = a.Evidence[0]
		}
		if !corpus.HasEvidence(a.Source) {
			return nil, nil, fmt.Errorf("memory %s source 不在输入证据中：%s", a.Name, a.Source)
		}
		switch a.Action {
		case "create":
			if strings.TrimSpace(a.Body) == "" {
				return nil, nil, fmt.Errorf("memory create 正文不能为空")
			}
			if a.Basis == "" {
				a.Basis = memory.BasisInferred
			}
			if a.Basis != memory.BasisExplicit && a.Basis != memory.BasisVerified && a.Basis != memory.BasisInferred {
				return nil, nil, fmt.Errorf("memory 非法 basis：%s", a.Basis)
			}
			if a.Kind == "" {
				a.Kind = "记录"
			}
		case "supersede":
			if a.Name == "" || a.Revision == "" || strings.TrimSpace(a.Body) == "" {
				return nil, nil, fmt.Errorf("memory supersede 需要 name/revision/body")
			}
		case "archive":
			if a.Name == "" || a.Revision == "" {
				return nil, nil, fmt.Errorf("memory archive 需要 name/revision")
			}
		default:
			return nil, nil, fmt.Errorf("memory 未知动作 %q", a.Action)
		}
	}
	return &plan, diffs, nil
}

func validateEvidence(evidence []string, corpus Corpus) error {
	if len(evidence) == 0 {
		return fmt.Errorf("缺少证据")
	}
	for _, ref := range evidence {
		if !corpus.HasEvidence(ref) {
			return fmt.Errorf("证据不在本次输入中：%s", ref)
		}
	}
	return nil
}

func normalizeKBLinks(links []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, id := range links {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !reKBID.MatchString(id) {
			return nil, fmt.Errorf("非法 kb id：%q", id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ApplyWiki 只应用 ParsePlan 产出的 write/delete；noop 不触碰磁盘。memory_actions 不在这里出现。
func ApplyWiki(store *wiki.Store, plan *Plan, diffs []DiffEntry) (int, error) {
	actions := make(map[string]WikiAction, len(plan.WikiActions))
	for _, a := range plan.WikiActions {
		actions[a.Name] = a
	}
	changed := 0
	for _, d := range diffs {
		if d.Status == "noop" {
			continue
		}
		a := actions[d.Name]
		var err error
		switch d.Status {
		case "write":
			_, err = store.Write(a.Name, a.Body, a.KBLinks, a.Revision)
		case "delete":
			err = store.Delete(a.Name, a.Revision)
		default:
			err = fmt.Errorf("未知 diff 状态：%s", d.Status)
		}
		if err != nil {
			return changed, fmt.Errorf("应用 wiki %s（已应用 %d 项）：%w", a.Name, changed, err)
		}
		changed++
	}
	return changed, nil
}

// Render 给 CLI 展示完整动作差异；memory 明确标记为“仅计划”。
func Render(result *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "persona=%s dreamer=%s messages=%d tokens=%d truncated=%v applied=%v changed=%d\n",
		result.Plan.Persona, result.Dreamer, len(result.Corpus.Messages), result.Corpus.Tokens,
		result.Corpus.Truncated, result.Applied, result.Changed)
	if result.Plan.Summary != "" {
		b.WriteString("\n" + result.Plan.Summary + "\n")
	}
	b.WriteString("\n[wiki diff]\n")
	if len(result.Diff) == 0 {
		b.WriteString("（无）\n")
	}
	for _, d := range result.Diff {
		fmt.Fprintf(&b, "- %s %s → %s\n", d.Action, d.Name, d.Status)
		if d.Status == "write" {
			if d.OldBody != "" {
				b.WriteString("  旧：\n" + indent(d.OldBody) + "\n")
			}
			b.WriteString("  新：\n" + indent(d.NewBody) + "\n")
		}
	}
	b.WriteString("\n[memory actions（仅计划，未应用）]\n")
	if len(result.Plan.MemoryActions) == 0 {
		b.WriteString("（无）\n")
	}
	for _, a := range result.Plan.MemoryActions {
		fmt.Fprintf(&b, "- %s %s source=%s evidence=%s\n    %s\n",
			a.Action, a.Name, a.Source, strings.Join(a.Evidence, ","), a.Body)
	}
	if result.AuditPath != "" {
		b.WriteString("\n审计：" + result.AuditPath + "\n")
	}
	return b.String()
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n")
}
