// Package dream：手动、单人格、可审计的离线记忆整理。
// 首轮只应用 wiki 变更；memory_actions 仅作为计划输出，绝不写入 memory。
package dream

import (
	"context"
	"errors"
	"strconv"
	"time"

	"xaihi/internal/llm"
)

var (
	ErrCrossPersona = errors.New("dreaming 不允许跨人格读取")
	ErrNoRange      = errors.New("dreaming 必须显式指定 --since 或 --conv")
)

type Options struct {
	Persona        string
	ConversationID string
	Since          time.Time
	MaxTokens      int
	Apply          bool
}

type InputRange struct {
	ConvID  string `json:"conv_id"`
	FromSeq int    `json:"from_seq"`
	ToSeq   int    `json:"to_seq"`
}

type SourceMessage struct {
	ConvID    string    `json:"conv_id"`
	Seq       int       `json:"seq"`
	Role      string    `json:"role"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func (m SourceMessage) Ref() string { return "conv:" + m.ConvID + "#" + strconv.Itoa(m.Seq) }

type Corpus struct {
	Persona   string          `json:"persona"`
	Messages  []SourceMessage `json:"messages"`
	Ranges    []InputRange    `json:"ranges"`
	Tokens    int             `json:"tokens"`
	Truncated bool            `json:"truncated"`
}

func (c Corpus) HasEvidence(ref string) bool {
	for _, m := range c.Messages {
		if m.Ref() == ref {
			return true
		}
	}
	return false
}

type WikiAction struct {
	Action   string   `json:"action"`
	Name     string   `json:"name"`
	Body     string   `json:"body,omitempty"`
	KBLinks  []string `json:"kb_links,omitempty"`
	Revision string   `json:"revision,omitempty"`
	Evidence []string `json:"evidence"`
}

type MemoryAction struct {
	Action   string   `json:"action"`
	Name     string   `json:"name,omitempty"`
	Body     string   `json:"body,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Basis    string   `json:"basis,omitempty"`
	Source   string   `json:"source,omitempty"`
	Revision string   `json:"revision,omitempty"`
	Evidence []string `json:"evidence"`
}

type Plan struct {
	Persona       string         `json:"persona"`
	Inputs        []InputRange   `json:"inputs"`
	Summary       string         `json:"summary"`
	WikiActions   []WikiAction   `json:"wiki_actions"`
	MemoryActions []MemoryAction `json:"memory_actions"`
}

type DiffEntry struct {
	Action  string `json:"action"`
	Name    string `json:"name"`
	Status  string `json:"status"` // write | delete | noop
	OldBody string `json:"old_body,omitempty"`
	NewBody string `json:"new_body,omitempty"`
}

type Result struct {
	Plan      *Plan       `json:"plan"`
	Corpus    Corpus      `json:"corpus"`
	Diff      []DiffEntry `json:"diff"`
	Dreamer   string      `json:"dreamer"`
	Applied   bool        `json:"applied"`
	Changed   int         `json:"changed"`
	AuditPath string      `json:"audit_path,omitempty"`
}

type ChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (string, error)
}

type ChatFunc func(ctx context.Context, req llm.ChatRequest) (string, error)

func (f ChatFunc) Chat(ctx context.Context, req llm.ChatRequest) (string, error) {
	return f(ctx, req)
}
