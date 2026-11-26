// Package agent：多轮对话循环（P2 才在这里加工具）。
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"xaihi/internal/config"
	"xaihi/internal/llm"
	"xaihi/internal/logx"
	"xaihi/internal/memory"
	"xaihi/internal/store"
	"xaihi/internal/wiki"
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
	mem       *memory.Store
	wiki      *wiki.Store
}

func New(cfg config.Config, st store.Store, profile config.Profile) *Agent {
	if profile.Model == "" {
		profile.Model = cfg.Model
	}
	// 人格可插拔：personas/<name>.md 拼在任务指令前面。
	// 拼在这里而不是拼装时，是因为"人格 + 指令"之后就不该再分开看了 ——
	// 也保证 estimateChars / guardSystem 看到的是最终内容。
	if persona, err := config.LoadPersona(profile.Persona); err == nil && persona != "" {
		if strings.TrimSpace(profile.System) != "" {
			profile.System = persona + "\n\n" + profile.System
		} else {
			profile.System = persona
		}
	}
	// 地址和密钥来自 profile 引用的接口（老数据没迁移时 Resolve 会逐级回落）
	r := profile.Resolve()
	// 跟随上游的配置：这一轮开始前先看一眼现在的能力。
	// 问不到就用配置里存的值 —— 那些是必填的，本来就是能用的兜底。
	if profile.AutoCaps {
		if info, ok := llm.CapsFor(r.BaseURL, r.APIKey, profile.Model); ok {
			if info.ContextWindow > 0 {
				profile.ContextWindow = info.ContextWindow
			}
			if info.MaxOutput > 0 {
				profile.MaxTokensOut = info.MaxOutput
			}
			if len(info.Input) > 0 {
				profile.Input = info.Input
			}
			if len(info.EffortLevels) > 0 {
				profile.EffortLevels = info.EffortLevels
			}
		}
	}
	base, key := r.BaseURL, r.APIKey
	if base == "" {
		base = cfg.BaseURL
	}
	if key == "" {
		key = cfg.APIKey
	}
	// memory/wiki 的主人不是会话 profile，而是它引用的人格 profile。
	// 多个人格存在时不猜测旧共享记忆的归属；唯一人格时才复制旧数据，原目录保留。
	var mem *memory.Store
	var wk *wiki.Store
	if profile.Persona != "" {
		pp, err := config.ResolvePersonaProfile(profile.Persona, profile.Persistent)
		if err != nil {
			logx.Warn("persona.profile_load name=%s err=%v；memory/wiki 已失败关闭", profile.Persona, err)
			pp = config.PersonaProfile{Name: profile.Persona}
		}
		if pp.Memory {
			root := memory.OwnerRoot(config.DataDir(), profile.Persona)
			if names := config.ListPersonas(); len(names) == 1 && names[0] == profile.Persona {
				if n, err := memory.MigrateLegacy(root, memory.DefaultRoot(config.DataDir())); err != nil {
					logx.Warn("memory.legacy_migrate owner=%s copied=%d err=%v", profile.Persona, n, err)
				} else if n > 0 {
					logx.Info("memory.legacy_migrate owner=%s copied=%d", profile.Persona, n)
				}
			}
			mem = memory.New(root)
		}
		if pp.Wiki {
			wk = wiki.New(wiki.OwnerRoot(config.DataDir(), profile.Persona))
		}
	}
	cl := llm.New(base, key)
	cl.SessionHeader = profile.SessionHeader
	cl.Headers = profile.Headers
	return &Agent{cfg: cfg, llm: cl, st: st, profile: profile, mem: mem, wiki: wk}
}

func (a *Agent) Profile() config.Profile { return a.profile }

// Memory 返回当前人格的记忆；未启用人格或 memory 时为 nil。
func (a *Agent) Memory() *memory.Store { return a.mem }

// Wiki 返回当前人格的可重建索引；未启用 wiki 时为 nil。
func (a *Agent) Wiki() *wiki.Store { return a.wiki }

// ModelID 是发给上游的模型名（profile.model 现在是复合键，不能直接发）
func (a *Agent) ModelID() string { return a.profile.Resolve().ModelID }

// Load 或新建会话。
func (a *Agent) Conversation(id string) (*store.Conversation, error) {
	id = strings.TrimSpace(id)
	if id != "" {
		c, err := a.st.Get(id)
		if err != nil {
			// 传了 id 就是「续这个会话」，读不到必须报错。
			// 原来的实现是读不到就静默新建 —— 前端把 id 拼错时，消息会悄悄写进
			// 一个新会话，用户以为还在原会话；顺带白烧一次模型调用。
			return nil, fmt.Errorf("会话 %q 不存在或读不出来：%w", id, err)
		}
		if c.Persona == "" {
			c.Persona = a.profile.Persona
		}
		return c, nil
	}
	// 只有明确不给 id 才是「新建」
	now := time.Now()
	return &store.Conversation{ID: a.st.NextID(), Title: "新会话", CreatedAt: now, UpdatedAt: now, Persona: a.profile.Persona}, nil
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
	err := a.llm.Stream(ctx, llm.ChatRequest{Model: a.ModelID(), Messages: msgs, ReasoningEffort: a.profile.ReasoningEffort}, func(ch llm.Chunk) error {
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
