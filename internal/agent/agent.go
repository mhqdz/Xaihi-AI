// Package agent：多轮对话循环（P2 才在这里加工具）。
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"agentd/internal/config"
	"agentd/internal/llm"
	"agentd/internal/store"
)

type Agent struct {
	cfg config.Config
	llm *llm.Client
	// Drain：取出本轮期间用户插话（web 层提供）；OnInterject：把插话回显给前端
	MaxRounds   int // 0=不限制（内部硬上限 100 兜底）
	Drain       func() []string
	OnInterject func(string)
	OnStats     func(llm.Stats) // 每轮结束后的上游速度/用量（可为 nil）
	// OnToolStart：工具开始执行（前端显示"执行中"卡片，长工具不至于看起来像卡死）
	OnToolStart func(name, args string)
	// OnStage：阶段性状态（compacting / generating），前端用来显示"压缩中/生成中"
	OnStage func(stage string)
	// OnSeq：流式消息预分配到的 seq（前端可以据此把流式气泡挂到正确位置）
	OnSeq func(seq int)
	// 交接：父会话派发任务 / 子会话汇报失败
	MsgOrigin string // 本轮用户消息的来源（user/system/timer/handoff）
	SpawnTask func(kind string, card map[string]any) (int64, error)
	FailTask  func(reason, evidence string) (string, error)
	st        store.Store
	profile   config.Profile
}

func New(cfg config.Config, st store.Store, profile config.Profile) *Agent {
	if profile.Model == "" {
		profile.Model = cfg.Model
	}
	if profile.BaseURL == "" {
		profile.BaseURL = cfg.BaseURL
	}
	key := profile.APIKey
	if key == "" {
		key = cfg.APIKey
	}
	return &Agent{cfg: cfg, llm: llm.New(profile.BaseURL, key), st: st, profile: profile}
}

func (a *Agent) Profile() config.Profile { return a.profile }

func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Load 或新建会话。
func (a *Agent) Conversation(id string) (*store.Conversation, error) {
	if id != "" {
		if c, err := a.st.Get(id); err == nil {
			return c, nil
		}
	}
	now := time.Now()
	return &store.Conversation{ID: a.st.NextID(), Title: "新会话", CreatedAt: now, UpdatedAt: now}, nil
}

// Send：追加用户消息 → 流式请求 → 追加助手消息 → 存盘。
// onDelta 用来把增量交给调用方（CLI 打印 / HTTP SSE 推送）。
func (a *Agent) Send(ctx context.Context, c *store.Conversation, text string,
	onDelta func(content, reasoning string)) error {
	c.Messages = append(c.Messages, store.Message{Role: "user", Content: text, CreatedAt: time.Now(), Origin: a.MsgOrigin})
	if c.Title == "新会话" || c.Title == "" {
		t := strings.TrimSpace(text)
		if r := []rune(t); len(r) > 24 {
			t = string(r[:24]) + "…"
		}
		c.Title = t
	}

	msgs := make([]llm.Message, 0, len(c.Messages))
	for _, m := range c.Messages {
		msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
	}

	var content, reasoning strings.Builder
	err := a.llm.Stream(ctx, llm.ChatRequest{Model: a.profile.Model, Messages: msgs, ReasoningEffort: a.profile.ReasoningEffort}, func(ch llm.Chunk) error {
		if ch.Done {
			return nil
		}
		content.WriteString(ch.Delta)
		reasoning.WriteString(ch.Reasoning)
		if onDelta != nil && (ch.Delta != "" || ch.Reasoning != "") {
			onDelta(ch.Delta, ch.Reasoning)
		}
		return nil
	})
	// 即使出错也把已收到的部分存下来，便于排查
	if content.Len() > 0 || err == nil {
		c.Messages = append(c.Messages, store.Message{
			Role: "assistant", Content: strings.TrimSpace(content.String()), Reasoning: reasoning.String(), CreatedAt: time.Now(),
		})
	}
	if serr := a.st.Save(c); serr != nil {
		return fmt.Errorf("存盘失败: %w", serr)
	}
	return err
}
