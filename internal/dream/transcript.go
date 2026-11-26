package dream

import (
	"context"
	"fmt"
	"strings"

	"xaihi/internal/store"
)

const (
	defaultMaxTokens = 20000
	toolResultRunes  = 800
	messageRunes     = 4000
)

// ReadTranscripts 构造 dreaming 的受限输入。范围必须显式给出，persona 必须与会话固化归属一致。
func ReadTranscripts(ctx context.Context, st store.Store, opts Options) (Corpus, error) {
	corpus := Corpus{Persona: opts.Persona}
	if st == nil {
		return corpus, fmt.Errorf("缺少会话存储")
	}
	if strings.TrimSpace(opts.Persona) == "" {
		return corpus, fmt.Errorf("必须指定 persona")
	}
	if opts.ConversationID == "" && opts.Since.IsZero() {
		return corpus, ErrNoRange
	}
	if opts.MaxTokens == 0 {
		opts.MaxTokens = defaultMaxTokens
	}
	if opts.MaxTokens < 100 {
		return corpus, fmt.Errorf("max-tokens 至少为 100")
	}
	convs, err := st.List()
	if err != nil {
		return corpus, err
	}
	found := false
	for _, brief := range convs {
		if opts.ConversationID != "" && brief.ID != opts.ConversationID {
			continue
		}
		found = true
		c, err := st.Get(brief.ID)
		if err != nil {
			return corpus, err
		}
		if c.Persona != opts.Persona {
			if opts.ConversationID != "" {
				return corpus, fmt.Errorf("%w：会话 %s 属于 %q，不是 %q", ErrCrossPersona, c.ID, c.Persona, opts.Persona)
			}
			continue
		}
		var rng InputRange
		for _, m := range c.Messages {
			select {
			case <-ctx.Done():
				return corpus, ctx.Err()
			default:
			}
			if m.Status == "running" || (m.Role != "user" && m.Role != "assistant" && m.Role != "tool") {
				continue
			}
			if !opts.Since.IsZero() && (m.CreatedAt.IsZero() || m.CreatedAt.Before(opts.Since)) {
				continue
			}
			text := strings.TrimSpace(m.Content) // reasoning / images / ToolResult 实体不进入输入
			if text == "" {
				continue
			}
			kind := "user_statement"
			limit := messageRunes
			if m.Role == "assistant" {
				kind = "assistant_statement"
			}
			if m.Role == "tool" {
				kind = "tool_observation"
				limit = toolResultRunes
			}
			text, truncated := capRunes(text, limit)
			if truncated {
				corpus.Truncated = true
			}
			sm := SourceMessage{ConvID: c.ID, Seq: m.Seq, Role: m.Role, Kind: kind, Text: text, CreatedAt: m.CreatedAt}
			cost := estimateTokens(text) + 12
			if corpus.Tokens+cost > opts.MaxTokens {
				corpus.Truncated = true
				if len(corpus.Messages) == 0 {
					return corpus, fmt.Errorf("max-tokens=%d 连首条转录也放不下", opts.MaxTokens)
				}
				break
			}
			corpus.Messages = append(corpus.Messages, sm)
			corpus.Tokens += cost
			if rng.ConvID == "" {
				rng = InputRange{ConvID: c.ID, FromSeq: m.Seq, ToSeq: m.Seq}
			} else {
				rng.ToSeq = m.Seq
			}
		}
		if rng.ConvID != "" {
			corpus.Ranges = append(corpus.Ranges, rng)
		}
		if corpus.Tokens >= opts.MaxTokens {
			corpus.Truncated = true
			break
		}
	}
	if opts.ConversationID != "" && !found {
		return corpus, fmt.Errorf("会话 %s 不存在", opts.ConversationID)
	}
	if len(corpus.Messages) == 0 {
		return corpus, fmt.Errorf("指定范围内没有可用于 dreaming 的转录")
	}
	return corpus, nil
}

func capRunes(s string, limit int) (string, bool) {
	r := []rune(s)
	if len(r) <= limit {
		return s, false
	}
	return string(r[:limit]) + "…[截断]", true
}

// 与 memory 的估算保持同一量级：CJK 每码点 1，ASCII 连续段约 4 字 1 token。
func estimateTokens(s string) int {
	n, ascii := 0, 0
	for _, r := range s {
		if r > 127 {
			n += (ascii+3)/4 + 1
			ascii = 0
		} else {
			ascii++
		}
	}
	return n + (ascii+3)/4
}
