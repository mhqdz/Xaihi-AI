package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentd/internal/config"
	"agentd/internal/llm"
	"agentd/internal/logx"
	"agentd/internal/store"
	"agentd/internal/tools"
)

// 工具循环：最多 8 轮，避免模型自己绕不出来。

// maxChars：该 profile 的上限，未配则用全局默认
// 上下文口径统一用 token：
//
//	· 配置项是 token（max_prompt_tokens）
//	· 内部按 charsPerToken 估算字符数，用于实际裁剪（模型侧没有分词器可调）
const (
	defaultMaxPromptTokens = 105000 // 默认输入上限（模型窗口 131072 的 ~80%）
	charsPerToken          = 1.8    // 中英混合经验值：1 token ≈ 1.8 字符
)

func (a *Agent) maxTokens() int {
	if a.profile.MaxPromptTokens > 0 {
		return a.profile.MaxPromptTokens
	}
	return defaultMaxPromptTokens
}

// MaxTokens：该 profile 的输入 token 上限（供接口展示）
func (a *Agent) MaxTokens() int { return a.maxTokens() }

// maxTokensOut：单次生成上限（含思考），默认 8192 —— 防止上游无限思考
func (a *Agent) maxTokensOut() int {
	if a.profile.MaxTokensOut > 0 {
		return a.profile.MaxTokensOut
	}
	return 8192
}

// reasoningBudget：单轮思考 token 预算（默认 4096）—— 引擎默认是"不限"，会跑飞
// reasoningEffort：openai 兼容的云端不认本地扩展时按需省略（provider=openai 仍发标准 effort）
func (a *Agent) reasoningEffort() string {
	if a.profile.Provider == "openai" {
		switch a.profile.ReasoningEffort {
		case "", "none":
			return ""
		}
	}
	return a.profile.ReasoningEffort
}

// reasoningBudgetParam：Strata 扩展字段，只有本地/未知 provider 才发
func (a *Agent) reasoningBudgetParam() int {
	if a.profile.Provider == "openai" {
		return 0
	}
	return a.reasoningBudget()
}

func (a *Agent) reasoningBudget() int {
	if a.profile.ReasoningBudget > 0 {
		return a.profile.ReasoningBudget
	}
	return 4096
}

func (a *Agent) maxChars() int { return int(float64(a.maxTokens()) * charsPerToken) }

// ContextWindow：该模型的窗口（tokens）
//
//	优先 profile.context_window → 上游 /v1/models 的 meta.n_ctx → 131072
func (a *Agent) ContextWindow() int {
	if a.profile.ContextWindow > 0 {
		return a.profile.ContextWindow
	}
	base, key, model := a.cfg.BaseURL, a.cfg.APIKey, a.cfg.Model
	if a.profile.BaseURL != "" {
		base = a.profile.BaseURL
	}
	if a.profile.Model != "" {
		model = a.profile.Model
	}
	if a.profile.APIKey != "" {
		key = a.profile.APIKey
	}
	if n := llm.ContextWindow(base, key, model); n > 0 {
		return n
	}
	return 131072
}

// CompactPct：达到上限的百分之几时做压缩/摘要（默认 80）
func (a *Agent) CompactPct() int {
	if a.profile.CompactPct > 0 && a.profile.CompactPct < 100 {
		return a.profile.CompactPct
	}
	return 80
}

// compactAt：该模型触发压缩的字符数
func (a *Agent) compactAt() int {
	return a.maxChars() * a.CompactPct() / 100
}

// flushInterjects：把队列中尚未消费的插话落库（作为 user 消息）。
// 场景：本轮没有工具轮次可插入，插话没有机会被 Drain ✓ 不落库就会丢/延迟到下一次请求。
func (a *Agent) flushInterjects(c *store.Conversation) {
	if a.Drain == nil {
		return
	}
	n := 0
	for _, t := range a.Drain() {
		if strings.TrimSpace(t) == "" {
			continue
		}
		c.Messages = append(c.Messages, store.Message{Role: "user", Content: t})
		n++
	}
	if n > 0 {
		_ = a.st.Save(c)
	}
}

// SendWithTools 带上工具跑一轮（含多轮工具调用）。并行执行同一轮里的多个工具。
func (a *Agent) SendWithTools(ctx context.Context, c *store.Conversation, text string,
	onDelta func(content, reasoning string), onTool func(name, args string, result any, ms int64)) error {
	reg := tools.Registry()
	if len(a.profile.Tools) > 0 {
		for name := range reg {
			keep := false
			for _, t := range a.profile.Tools {
				if t == name {
					keep = true
					break
				}
			}
			if !keep {
				delete(reg, name)
			}
		}
	}
	specs := tools.SchemaJSON(reg)

	c.Messages = append(c.Messages, store.Message{Role: "user", Content: text, Origin: a.MsgOrigin})
	if c.Title == "新会话" || c.Title == "" {
		t := strings.TrimSpace(text)
		if r := []rune(t); len(r) > 24 {
			t = string(r[:24]) + "…"
		}
		c.Title = t
	}
	// 用户消息立刻落盘：请求被中断也不会丢这句。
	// 这里不能吞错误 —— 起点都没写进去，后面跑完整轮也只是在烧模型，
	// 而且前端拿到的 meta 里 conv_id 会是空的（新建会话的 ID 就是 Save 分配的）。
	if err := a.st.Save(c); err != nil {
		return fmt.Errorf("保存用户消息失败：%w", err)
	}

	limit := a.MaxRounds
	if limit <= 0 {
		limit = int(^uint(0) >> 1) // 关闭限制 = 不设上限（由用户自行掌控）
	}
	lastSig, sameCallStreak := "", 0
	for round := 0; round < limit; round++ {
		// 中途插话：把用户新消息插进本轮上下文（模型这一轮就能看到）
		if a.Drain != nil {
			for _, t := range a.Drain() {
				if strings.TrimSpace(t) == "" {
					continue
				}
				c.Messages = append(c.Messages, store.Message{Role: "user", Content: t})
				if a.OnInterject != nil {
					a.OnInterject(t)
				}
			}
		}
		// 超阈值就先做一次摘要压缩（带检查点），再走轻量裁剪兜底
		keep := a.profile.KeepMessages
		if keep <= 0 {
			keep = a.cfg.KeepMessages
		}
		// 只有真会压缩时才报 compacting。原来是无条件发，于是每轮都闪一下
		// 「压缩中…（上下文超阈值，正在写摘要）」—— 新会话离阈值还差得远，
		// 这句话是假的（maybeSummarize 内部自己会判断阈值，直接返回）。
		if a.willSummarize(c) && a.OnStage != nil {
			a.OnStage("compacting")
		}
		_ = a.maybeSummarize(ctx, c, keep)
		if a.OnStage != nil {
			a.OnStage("generating")
		}
		msgs, toolMsgs := toWire(c.Messages, reg)
		before := len(msgs)
		beforeChars := 0
		for _, m := range msgs {
			beforeChars += wireLen(m.Content)
		}
		msgs = compact(msgs, keep, a.compactAt())
		if before != len(msgs) {
			afterChars := 0
			for _, m := range msgs {
				afterChars += wireLen(m.Content)
			}
			logx.Info("compact conv=%s msgs=%d->%d chars=%d->%d budget=%d keep=%d",
				c.ID, before, len(msgs), beforeChars, afterChars, a.compactAt(), keep)
		}
		if a.profile.System != "" || c.Summary != "" || len(c.KV) > 0 {
			sys := a.profile.System
			if len(c.KV) > 0 {
				sys += "\n\n[已知事实]\n"
				keys := make([]string, 0, len(c.KV))
				for k := range c.KV {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					sys += "- " + k + ": " + c.KV[k] + "\n"
				}
			}
			pfx := make([]llm.Message, 0, 2)
			pfx = append(pfx, llm.Message{Role: "system", Content: sys})
			if c.Summary != "" {
				pfx = append(pfx, llm.Message{Role: "user", Content: "[更早对话的摘要]\n" + c.Summary})
			}
			msgs = append(pfx, msgs...)
		}
		total := 0
		for _, m := range msgs {
			total += wireLen(m.Content)
			for _, tc := range m.ToolCalls {
				total += len(tc.Function.Arguments)
			}
		}
		if a.maxChars() > 0 && total > a.maxChars() {
			return fmt.Errorf("请求体已达 %d 字符（上限 %d），已中止以免爆上下文；建议新开会话",
				total, a.maxChars())
		}
		var content, reasoning strings.Builder
		var reasonStart, reasonMs int64
		var calls []llm.ToolCall
		// 流式预分配：第一条正文到达时先 INSERT 一条 running 行占住 seq，
		// 之后每 1s 落一次盘。进程被 kill 时库里至少留一条 failed，而不是整条丢失。
		pendingSeq := 0
		var lastFlush time.Time
		convID64, _ := strconv.ParseInt(c.ID, 10, 64)
		flushPending := func(status string) {
			if pendingSeq == 0 || convID64 == 0 {
				return
			}
			_ = a.st.UpdateMessage(convID64, pendingSeq, strings.TrimSpace(content.String()), reasoning.String(), reasonMs, status)
		}
		if a.OnStage != nil {
			a.OnStage("generating")
		}
		err := a.llm.StreamWithTools(ctx, llm.ChatRequest{Model: a.profile.Model, Messages: msgs, Tools: specs, MaxTokens: a.maxTokensOut(), ReasoningEffort: a.reasoningEffort(), ReasoningBudget: a.reasoningBudgetParam()},
			func(ch llm.Chunk) error {
				if ch.Done {
					return nil
				}
				now := time.Now().UnixMilli()
				if ch.Reasoning != "" && reasonStart == 0 {
					reasonStart = now
				}
				if ch.Delta != "" && reasonStart != 0 && reasonMs == 0 {
					reasonMs = now - reasonStart
				}
				content.WriteString(ch.Delta)
				reasoning.WriteString(ch.Reasoning)
				if ch.Delta != "" && pendingSeq == 0 && convID64 != 0 {
					if seq, e := a.st.InsertPending(convID64, "assistant"); e == nil {
						pendingSeq = seq
						lastFlush = time.Now()
						if a.OnSeq != nil {
							a.OnSeq(seq)
						}
					}
				}
				if pendingSeq != 0 && time.Since(lastFlush) >= time.Second {
					flushPending("running")
					lastFlush = time.Now()
				}
				if onDelta != nil && (ch.Delta != "" || ch.Reasoning != "") {
					onDelta(ch.Delta, ch.Reasoning)
				}
				return nil
			}, &calls)
		// 收尾计时：这一轮结束（正常结束、只有 tool_calls、或被中断）时停表，
		// 否则工具轮（没有 content 增量）会永远是 0 ✓
		if a.OnStats != nil {
			a.OnStats(a.llm.Stats())
		}
		if reasonStart != 0 && reasonMs == 0 {
			reasonMs = time.Now().UnixMilli() - reasonStart
		}
		if err != nil {
			// 意外中断（用户停止 / 网络断 / 上游报错）：保住已生成的内容，用 status 标记状态，
			// 不再把“——（中断：…）”塞进正文（说明交给前端按 status 渲染）。
			st := "interrupted"
			if !errors.Is(err, context.Canceled) {
				st = "failed"
			}
			body := strings.TrimSpace(content.String())
			if body != "" || reasoning.Len() > 0 {
				flushPending(st)
				c.Messages = append(c.Messages, store.Message{
					Role: "assistant", Content: body, Reasoning: reasoning.String(), ReasoningMs: reasonMs, Status: st, Seq: pendingSeq,
				})
			}
			if body == "" {
				// 一个字都没出就断了：补一条说明，前端仍能看到“这一轮被中断”
				note := "（已停止）"
				if !errors.Is(err, context.Canceled) {
					note = "（中断：" + err.Error() + "）"
				}
				c.Messages = append(c.Messages, store.Message{Role: "assistant", Content: note, Status: st})
			}
			if e := a.st.Save(c); e != nil { // 中断也要留住上下文（用户消息 / 已完成的工具轮次）
				logx.Warn("中断后保存上下文失败 conv=%s: %v", c.ID, e)
			}
			a.flushInterjects(c)
			return err
		}
		if len(calls) == 0 {
			flushPending("done")
			c.Messages = append(c.Messages, store.Message{
				Role: "assistant", Content: strings.TrimSpace(content.String()), Reasoning: reasoning.String(), ReasoningMs: reasonMs,
				Status: "done", Seq: pendingSeq})
			if err := a.st.Save(c); err != nil {
				return err
			}
			a.flushInterjects(c) // 本轮没有工具轮次 → 未消费的插话落库，下轮可见
			return nil
		}
		// 跑飞检测：连续 3 轮调用完全相同的工具与参数 → 视为空转，收尾并说明
		if len(calls) > 0 {
			sig := calls[0].Function.Name + calls[0].Function.Arguments
			if sig == lastSig {
				sameCallStreak++
			} else {
				lastSig, sameCallStreak = sig, 1
			}
			if sameCallStreak >= 3 {
				if pendingSeq != 0 {
					_ = a.st.DeleteMessage(convID64, pendingSeq)
					pendingSeq = 0
				}
				note := "（检测到连续 3 轮调用同一个工具、同样参数，已中止以免空转。可以换个说法让我继续。）"
				c.Messages = append(c.Messages, store.Message{Role: "assistant", Content: note})
				_ = a.st.Save(c)
				if onDelta != nil {
					onDelta(note, "")
				}
				return nil
			}
		}

		// 记录带 tool_calls 的助手消息
		// 工具轮放弃流式占位行（UpdateMessage 不写 tool_calls），改走下面的整体插入
		if pendingSeq != 0 {
			_ = a.st.DeleteMessage(convID64, pendingSeq)
			pendingSeq = 0
		}
		c.Messages = append(c.Messages, store.Message{
			Role: "assistant", Content: strings.TrimSpace(content.String()),
			Reasoning: reasoning.String(), ReasoningMs: reasonMs, ToolCalls: calls})
		// 工具轮：先落库，再执行（中断也不丢）。
		// 写不进去就停 —— 否则这一轮真的会去执行工具，但历史里没有对应的
		// tool_calls，下次读回来上下文是断的。
		if err := a.st.Save(c); err != nil {
			return fmt.Errorf("保存工具调用失败：%w", err)
		}
		// 并行执行
		results := make([]any, len(calls))
		toolMs := make([]int64, len(calls))
		var wg sync.WaitGroup
		for i, call := range calls {
			wg.Add(1)
			go func(i int, call llm.ToolCall) {
				defer wg.Done()
				spec, ok := reg[call.Function.Name]
				if !ok {
					results[i] = "✗ 没有这个工具：" + call.Function.Name
					return
				}
				var args map[string]any
				_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
				if args == nil {
					args = map[string]any{}
				}
				if a.OnToolStart != nil {
					a.OnToolStart(call.Function.Name, call.Function.Arguments)
				}
				t0 := time.Now()
				res, err := spec.Run(ctx, args, &tools.Env{Messages: c.Messages, SpawnTask: a.SpawnTask, FailTask: a.FailTask})
				ms := time.Since(t0).Milliseconds()
				toolMs[i] = ms
				logx.Info("tool.run name=%s ms=%d", call.Function.Name, ms)
				if err != nil {
					res = "✗ " + err.Error()
				}
				results[i] = res
				if onTool != nil {
					onTool(call.Function.Name, call.Function.Arguments, res, ms)
				}
			}(i, call)
			_ = toolMsgs
		}
		wg.Wait()
		for i, call := range calls {
			textOut, imgs := splitResult(results[i])
			c.Messages = append(c.Messages, store.Message{
				Role: "tool", ToolCallID: call.ID, Content: textOut, ToolResult: results[i], Images: imgs, ToolMs: toolMs[i]})
			if len(imgs) > 0 {
				// OpenAI 的 tool 消息不能带图 → 用一条 user 消息把像素喂给模型
				c.Messages = append(c.Messages, store.Message{
					Role: "user", Content: "（上一个工具的截图，请以图为准）", Images: imgs})
			}
		}
		_ = a.st.Save(c) // 工具结果落库
	}
	// 轮次用尽：不能静默返回空回复 ✗ —— 补一条说明并落库
	note := fmt.Sprintf("（已达到工具轮次上限 %d 轮，本轮没有给出最终结论。可以追问一句\"继续\"让它接着做，或到设置里调高上限。）", limit)
	c.Messages = append(c.Messages, store.Message{Role: "assistant", Content: note})
	if err := a.st.Save(c); err != nil {
		return err
	}
	a.flushInterjects(c)
	if onDelta != nil {
		onDelta(note, "")
	}
	return nil
}

func toWire(msgs []store.Message, reg map[string]*tools.Spec) ([]llm.Message, bool) {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		var content any = m.Content
		if len(m.Images) > 0 {
			parts := []map[string]any{{"type": "text", "text": m.Content}}
			for _, img := range m.Images {
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": inlineImage(img)}})
			}
			content = parts
		}
		out = append(out, llm.Message{Role: m.Role, Content: content, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID})
	}
	return out, true
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// compact：上下文压缩（轻量、可预测）。
// 规则：保留 system + 最近 keep 条消息；更早的折叠成一条"省略说明"。
// 不调用模型做摘要（省钱、可预测）；需要更聪明的摘要时再换。
func compact(msgs []llm.Message, keep int, budget int) []llm.Message {
	var sys *llm.Message
	start := 0
	if len(msgs) > 0 && msgs[0].Role == "system" {
		sys = &msgs[0]
		start = 1
	}
	body := msgs[start:]
	total := 0
	for _, m := range body {
		total += wireLen(m.Content)
	}
	if keep <= 0 {
		keep = 12
	}
	// 只在"超出预算"时才压缩（对齐 Open WebUI 的按比例保留）：
	// 之前是 len(body) > keep 就砍到 16 条 ✗ 工具轮很快就把最近几步砍掉了 ✓
	if total <= budget {
		return msgs
	}
	// 从最早开始砍，直到剩余内容不超过预算的一半（保留最近一大段上下文）
	cut := 0
	remain := total
	for cut < len(body) && remain > budget/2 {
		remain -= wireLen(body[cut].Content)
		cut++
	}
	// 保留下限：没超预算时以 keep 为目标；超预算时必须能砍到预算内 ✗
	// （之前固定"至少留 keep=16 条"→ 消息数<16 时完全砍不动 → 请求体超上限被拒 ✓ 复现于 conv 22）
	minKeep := keep
	if total > budget && minKeep > 4 {
		minKeep = 4 // 至少留 4 条（约 2 轮），其余按预算砍
	}
	if max := len(body) - minKeep; cut > max {
		cut = max
	}
	if cut <= 0 {
		return msgs
	}
	// 边界不能落在 tool 上：那会把 tool 结果与它的 tool_call 拆开
	// （之前要求必须落在 user 上 ✗ 只有一条 user 时会一路退到 0，等于完全不压缩 ✓ 复现于 conv 24）
	for cut > 0 && body[cut].Role == "tool" {
		cut--
	}
	if cut <= 0 {
		return msgs
	}
	var done []string
	for _, m := range body[:cut] {
		for _, tc := range m.ToolCalls {
			args := strings.ReplaceAll(tc.Function.Arguments, "\n", " ")
			if len(args) > 80 {
				args = args[:80] + "…"
			}
			done = append(done, "- "+tc.Function.Name+" "+args)
		}
	}
	if len(done) > 12 {
		done = done[len(done)-12:]
	}
	noteText := fmt.Sprintf("（上下文压缩：更早的 %d 条消息已省略。", cut)
	if len(done) > 0 {
		noteText += "这些工具调用已经执行过，结果是当时就地看过的，不要重复执行：\n" + strings.Join(done, "\n")
	}
	noteText += "\n如果缺少必要信息，请向用户确认，不要猜。）"
	note := llm.Message{Role: "user", Content: noteText}
	out := make([]llm.Message, 0, keep+2)
	if sys != nil {
		out = append(out, *sys)
	}
	out = append(out, note)
	out = append(out, body[cut:]...)
	return out
}

// willSummarize：本轮是否真的会触发摘要压缩。和 maybeSummarize 的入口条件保持一致
// （超过 compactAt），用来决定要不要给前端发 compacting 阶段。
func (a *Agent) willSummarize(c *store.Conversation) bool {
	total := 0
	for _, m := range c.Messages {
		total += wireLen(m.Content)
	}
	return total >= a.compactAt()
}

// maybeSummarize：超过预算的一半时，把"最近 keep 条之前"的消息交给摘要模型，
// 生成检查点（summary + upto + model + ts）。失败不阻断本轮（降级为轻量裁剪）。
func (a *Agent) maybeSummarize(ctx context.Context, c *store.Conversation, keep int) error {
	total := 0
	for _, m := range c.Messages {
		total += wireLen(m.Content)
	}
	if total < a.compactAt() {
		return nil
	}
	cut := len(c.Messages) - keep
	if cut <= 0 {
		return nil
	}
	for cut < len(c.Messages) && c.Messages[cut].Role != "user" {
		cut++
	}
	if cut <= 0 {
		return nil
	}
	var b strings.Builder
	for _, m := range c.Messages[:cut] {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(tailRunes(m.Content, 400))
		b.WriteString("\n")
	}
	prompt := "把下面的对话压缩成不超过 400 字的摘要，供后续继续工作使用。务必保留：" +
		"任务目标、已确认的决策、涉及的文件路径与服务端口、遇到的问题与结论、下一步。" +
		"不要编造；不确定就写\"不确定\"。\n\n" + b.String()

	// 摘要模型：profile.Summarizer 指定，否则用自己
	sumProfile := a.profile
	if a.profile.Summarizer != "" {
		if p, ok := config.GetProfile(a.profile.Summarizer); ok {
			sumProfile = p
		}
	}
	sumKey := sumProfile.APIKey
	if sumKey == "" {
		sumKey = a.cfg.APIKey
	}
	sc := llm.New(sumProfile.BaseURL, sumKey)
	out, err := sc.Chat(ctx, llm.ChatRequest{Model: sumProfile.Model, ReasoningEffort: sumProfile.ReasoningEffort,
		Messages: []llm.Message{{Role: "user", Content: prompt}}})
	if err != nil || strings.TrimSpace(out) == "" {
		return err
	}
	c.Summary = strings.TrimSpace(out)
	c.SummaryUpto = c.Messages[cut-1].Seq
	c.SummaryModel = sumProfile.Model
	c.SummaryAt = time.Now()
	// KV：先只放"生成者"，其余由后续从摘要里抽（下一轮做）
	if c.KV == nil {
		c.KV = map[string]string{}
	}
	c.KV["summary_generated_by"] = sumProfile.Model + " @ " + c.SummaryAt.Format("2006-01-02 15:04")
	return a.st.Save(c)
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// splitResult：工具结果是 []map{type:text|image} 时拆成 文本 + 图片列表。
func splitResult(v any) (string, []string) {
	parts, ok := v.([]map[string]any)
	if !ok {
		return stringify(v), nil
	}
	var text string
	var imgs []string
	for _, p := range parts {
		switch p["type"] {
		case "text":
			if t, ok := p["text"].(string); ok {
				text += t
			}
		case "image":
			if u, ok := p["url"].(string); ok {
				imgs = append(imgs, u)
			}
		}
	}
	if text == "" {
		text = "（工具返回了一张图片）"
	}
	return text, imgs
}

// wireLen：Content 可能是 string 或 []parts，估算字符数即可（只用于预算判断）。
// 统一按"字符(rune)"计数：中文 1 字 = 1，不再用字节数
// （之前 len(string) 是字节数，中文会被放大 ~3 倍，导致 token 估算虚高、raw 与 chars 不可比）
func wireLen(v any) int {
	switch t := v.(type) {
	case string:
		return utf8.RuneCountInString(t)
	case []map[string]any:
		n := 0
		for _, p := range t {
			if s, ok := p["text"].(string); ok {
				n += utf8.RuneCountInString(s)
			}
		}
		return n
	}
	return 0
}

// inlineImage：把 /shots/… 读成 data URL；本来就是 data: 的原样返回
func inlineImage(img string) string {
	if strings.HasPrefix(img, "data:") || img == "" {
		return img
	}
	if strings.HasPrefix(img, "/shots/") {
		b, err := os.ReadFile(config.DataPath(img))
		if err != nil {
			return ""
		}
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
	}
	return img
}

// ContextSize 返回"当前会发给模型的字符数"与上限（与循环里的限额检查同一算法）。
// 供前端显示上下文占用，判断大概什么时候会被截断/压缩。
func (a *Agent) ContextSize(c *store.Conversation) (int, int) {
	reg := tools.Registry()
	if len(a.profile.Tools) > 0 {
		for name := range reg {
			keep := false
			for _, t := range a.profile.Tools {
				if t == name {
					keep = true
					break
				}
			}
			if !keep {
				delete(reg, name)
			}
		}
	}
	keep := a.profile.KeepMessages
	if keep <= 0 {
		keep = a.cfg.KeepMessages
	}
	msgs, _ := toWire(c.Messages, reg)
	msgs = compact(msgs, keep, a.compactAt())
	if a.profile.System != "" || c.Summary != "" || len(c.KV) > 0 {
		msgs = append([]llm.Message{{Role: "system", Content: a.profile.System}}, msgs...)
	}
	total := 0
	for _, m := range msgs {
		total += wireLen(m.Content)
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments)
		}
	}
	return total, a.maxChars()
}
