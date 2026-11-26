package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xaihi/internal/config"
	"xaihi/internal/llm"
	"xaihi/internal/memory"
	"xaihi/internal/store"
	"xaihi/internal/wiki"
)

type Runner struct {
	Store   store.Store
	Chat    ChatClient
	DataDir string
	Now     func() time.Time
}

type AuditRecord struct {
	CreatedAt           time.Time    `json:"created_at"`
	Persona             string       `json:"persona"`
	Dreamer             string       `json:"dreamer"`
	Inputs              []InputRange `json:"inputs"`
	TranscriptTokens    int          `json:"transcript_tokens"`
	TranscriptTruncated bool         `json:"transcript_truncated"`
	Plan                *Plan        `json:"plan"`
	Diff                []DiffEntry  `json:"diff"`
	Changed             int          `json:"changed"`
}

func (r *Runner) Run(ctx context.Context, opts Options) (*Result, error) {
	opts.Persona = strings.TrimSpace(opts.Persona)
	if opts.Persona == "" {
		return nil, fmt.Errorf("必须指定 --persona")
	}
	if opts.ConversationID == "" && opts.Since.IsZero() {
		return nil, ErrNoRange
	}
	dataDir := r.DataDir
	if dataDir == "" {
		dataDir = config.DataDir()
	}
	pp, err := config.PersonaProfileFor(opts.Persona)
	if err != nil {
		return nil, err
	}
	if !pp.Wiki {
		return nil, fmt.Errorf("人格 %s 未开启 wiki", opts.Persona)
	}
	sp, err := pp.DreamerProfile()
	if err != nil {
		return nil, err
	}
	corpus, err := ReadTranscripts(ctx, r.Store, opts)
	if err != nil {
		return nil, err
	}
	var memories []memory.Entry
	if pp.Memory {
		memories, err = memory.New(memory.OwnerRoot(dataDir, opts.Persona)).List()
		if err != nil {
			return nil, fmt.Errorf("读取人格 memory：%w", err)
		}
	}
	wikiStore := wiki.New(wiki.OwnerRoot(dataDir, opts.Persona))
	pages, err := wikiStore.List()
	if err != nil {
		return nil, fmt.Errorf("读取人格 wiki：%w", err)
	}
	prompt, err := buildPrompt(corpus, memories, pages)
	if err != nil {
		return nil, err
	}
	chat := r.Chat
	if chat == nil {
		resolved := sp.Resolve()
		client := llm.New(resolved.BaseURL, resolved.APIKey)
		client.Headers = sp.Headers
		chat = client
	}
	maxOut := sp.MaxTokensOut
	if maxOut <= 0 {
		maxOut = 8192
	}
	raw, err := chat.Chat(ctx, llm.ChatRequest{
		Model: sp.Resolve().ModelID, MaxTokens: maxOut,
		Messages:        []llm.Message{{Role: "system", Content: dreamInstructions}, {Role: "user", Content: prompt}},
		ReasoningEffort: sp.ReasoningEffort, ReasoningBudget: sp.ReasoningBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("dreamer 调用失败：%w", err)
	}
	plan, diffs, err := ParsePlan(raw, corpus, pages)
	if err != nil {
		return nil, err
	}
	result := &Result{Plan: plan, Corpus: corpus, Diff: diffs, Dreamer: sp.Name, Applied: opts.Apply}
	if opts.Apply {
		result.Changed, err = ApplyWiki(wikiStore, plan, diffs)
		if err != nil {
			return result, err
		}
		if err := r.writeAudit(result); err != nil {
			return result, err
		}
	}
	return result, nil
}

const dreamInstructions = `你是 Xaihi 的离线记忆整理器。你只处理当前人格的输入，不能读取或推断其他人格的数据。
只输出一个 JSON 对象，不要 Markdown 围栏或解释。schema：
{"persona":"...","inputs":[],"summary":"...","wiki_actions":[{"action":"write|delete","name":"a-z0-9_-","body":"...","kb_links":[],"revision":"...","evidence":["conv:id#seq"]}],"memory_actions":[{"action":"create|supersede|archive","name":"","body":"","kind":"","basis":"explicit|verified|inferred","source":"conv:id#seq","revision":"","evidence":["conv:id#seq"]}]}
规则：
1. wiki 是通用主题索引，围绕人物、项目、问题、经验和来源建页，不按日期复制日记。可以维护一个简洁 index 页面。
2. 每个 wiki/memory 动作必须引用真实出现在输入中的 evidence；没有证据就不做动作。禁止虚构 conv_id、seq、kb id、revision 或事实。
3. 新建 wiki 页面 revision 留空；修改或删除已有页面必须原样携带输入给出的 revision。不要对同一页面给多个动作。
4. memory_actions 只是供人工检查的计划，不会应用。只有确实值得新增、替代或归档记忆时才输出，保留来源和依据，不要推测升级 verified。
5. kb 只允许作为 id 链接，不复制或修改人审 kb，不修改 persona，不提出跨人格读取。
6. 没有值得整理的内容时，两个动作数组都返回空。单次每类动作最多 50 个。`

func buildPrompt(corpus Corpus, memories []memory.Entry, pages []wiki.Entry) (string, error) {
	type memoryView struct {
		Name, Body, Kind, Basis, Source, Revision string
	}
	type wikiView struct {
		Name, Body, Revision string
		KBLinks              []string
	}
	mems := make([]memoryView, 0, len(memories))
	for _, e := range memories {
		mems = append(mems, memoryView{e.Name, e.Body, e.Field(memory.FKind), e.Field(memory.FBasis), e.Field(memory.FSource), e.Revision})
	}
	views := make([]wikiView, 0, len(pages))
	for i, e := range pages {
		if i >= 50 {
			break
		}
		body := e.Body
		if len([]rune(body)) > 2000 {
			body = string([]rune(body)[:2000]) + "…[截断]"
		}
		views = append(views, wikiView{e.Name, body, e.Revision, e.KBLinks})
	}
	payload := map[string]any{
		"persona": corpus.Persona, "transcripts": corpus.Messages,
		"memory": mems, "wiki": views,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (r *Runner) writeAudit(result *Result) error {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	dataDir := r.DataDir
	if dataDir == "" {
		dataDir = config.DataDir()
	}
	dir := filepath.Join(dataDir, "dreams", "personas", result.Plan.Persona)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	record := AuditRecord{
		CreatedAt: now, Persona: result.Plan.Persona, Dreamer: result.Dreamer,
		Inputs: result.Plan.Inputs, TranscriptTokens: result.Corpus.Tokens,
		TranscriptTruncated: result.Corpus.Truncated, Plan: result.Plan, Diff: result.Diff, Changed: result.Changed,
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, now.Format("20060102-150405.000000000")+".json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("写 dreaming 审计：%w", err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	result.AuditPath = path
	return nil
}
