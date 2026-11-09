// Package store：P0 用 JSON 文件存会话（接口留好，P1 可换 sqlite）。
package store

import (
	"encoding/json"
	"fmt"

	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"xaihi/internal/llm"
)

type Message struct {
	Seq         int            `json:"seq"`
	Role        string         `json:"role"`
	Content     string         `json:"content"`
	Reasoning   string         `json:"reasoning,omitempty"`
	ReasoningMs int64          `json:"reasoning_ms,omitempty"`
	ToolCalls   []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	ToolResult  any            `json:"tool_result,omitempty"`
	Images      []string       `json:"images,omitempty"` // data:image/...;base64（工具返回的图 ✓）
	CreatedAt   time.Time      `json:"created_at"`
	Origin      string         `json:"origin,omitempty"`  // user | system | timer | handoff
	ToolMs      int64          `json:"tool_ms,omitempty"` // 工具执行耗时（tool 消息用）
	// 消息状态：running | done | interrupted | failed；空串 = 历史数据（按 done 处理）
	Status string `json:"status,omitempty"`
	// 派生字段（不落库）：sysnote | ""，由 web 层读出时计算，前端不再靠正则猜
	Kind string `json:"kind,omitempty"`
}

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  []Message `json:"messages"`
	MsgCount  int       `json:"-"`                   // 列表用的消息条数（不加载消息）
	ParentID  int64     `json:"parent_id,omitempty"` // 0 = 顶层
	Kind      string    `json:"kind,omitempty"`      // chat | child

	// 压缩检查点（模型可换、可追溯）
	Summary      string            `json:"summary,omitempty"`
	SummaryUpto  int               `json:"summary_upto,omitempty"` // 压到第几条 Seq
	SummaryModel string            `json:"summary_model,omitempty"`
	SummaryAt    time.Time         `json:"summary_at,omitempty"`
	KV           map[string]string `json:"kv,omitempty"` // 5 类固定字段 + generated_by
}

type Store interface {
	Get(id string) (*Conversation, error)
	Save(c *Conversation) error
	List() ([]*Conversation, error)
	ListArchived() ([]*Conversation, error)
	Archive(id string) error
	ArchiveInactiveBefore(t time.Time) (int, error)
	Restore(id string) error
	Delete(id string, hard bool) error
	NextID() string

	// 流式预分配：先占一行（status=running），之后按 seq 回填内容。
	// 不支持增量的实现（JSONStore）可以返回错误，调用方会退回“结束时一次性 Save”。
	InsertPending(convID int64, role string) (int, error)
	UpdateMessage(convID int64, seq int, content, reasoning string, reasoningMs int64, status string) error
	DeleteMessage(convID int64, seq int) error
}

type JSONStore struct{ Dir string }

func NewJSON(dir string) (*JSONStore, error) {
	for _, sub := range []string{"live", "archive"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &JSONStore{Dir: dir}, nil
}

func (s *JSONStore) liveDir() string { return filepath.Join(s.Dir, "live") }

func (s *JSONStore) file(id string) string { return filepath.Join(s.liveDir(), "conv-"+id+".json") }

// InsertPending：JSONStore 不做流式增量落盘（整份文件重写代价太高），
// 返回错误让调用方退回“结束时一次性 Save”。
func (s *JSONStore) InsertPending(convID int64, role string) (int, error) {
	return 0, fmt.Errorf("JSONStore 不支持流式预分配")
}

// UpdateMessage：同上，空操作。
func (s *JSONStore) UpdateMessage(convID int64, seq int, content, reasoning string, reasoningMs int64, status string) error {
	return nil
}

// DeleteMessage：同上，空操作。
func (s *JSONStore) DeleteMessage(convID int64, seq int) error { return nil }

func (s *JSONStore) Get(id string) (*Conversation, error) {
	b, err := os.ReadFile(s.file(id))
	if err != nil {
		return nil, err
	}
	var c Conversation
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	// 兼容老文件：没有 seq 的按数组顺序补上（只补一次，下次 Save 会写回）
	need := false
	for i := range c.Messages {
		if c.Messages[i].Seq == 0 {
			c.Messages[i].Seq = i + 1
			need = true
		}
	}
	_ = need
	return &c, nil
}

var seqMu sync.Mutex

func (s *JSONStore) Save(c *Conversation) error {
	seqMu.Lock() // 单进程内串行；跨进程靠文件 flock（writeProfiles 那套同款思路）
	maxSeq := 0
	for _, m := range c.Messages {
		if m.Seq > maxSeq {
			maxSeq = m.Seq
		}
	}
	for i := range c.Messages {
		if c.Messages[i].Seq == 0 {
			maxSeq++
			c.Messages[i].Seq = maxSeq
		}
	}
	seqMu.Unlock()
	c.UpdatedAt = time.Now()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.file(c.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.file(c.ID)) // 原子替换
}

func (s *JSONStore) archiveDir() string { return filepath.Join(s.Dir, "archive") }

func (s *JSONStore) ListArchived() ([]*Conversation, error) {
	dir := s.archiveDir()
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Conversation
	for _, e := range ents {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var c Conversation
		if json.Unmarshal(b, &c) == nil {
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// ArchiveInactiveBefore：JSON 实现（按 UpdatedAt 判断）
func (s *JSONStore) ArchiveInactiveBefore(t time.Time) (int, error) {
	list, err := s.List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range list {
		if c.UpdatedAt.Before(t) {
			if err := s.Archive(c.ID); err == nil {
				n++
			}
		}
	}
	return n, nil
}

func (s *JSONStore) Archive(id string) error {
	_ = os.MkdirAll(s.archiveDir(), 0o755)
	return os.Rename(s.file(id), filepath.Join(s.archiveDir(), "conv-"+id+".json"))
}

func (s *JSONStore) Restore(id string) error {
	return os.Rename(filepath.Join(s.archiveDir(), "conv-"+id+".json"), s.file(id))
}

// Delete：hard=false 时只归档（可恢复），hard=true 才真删（含归档副本）。
func (s *JSONStore) Delete(id string, hard bool) error {
	if !hard {
		return s.Archive(id)
	}
	_ = os.Remove(filepath.Join(s.archiveDir(), "conv-"+id+".json"))
	return os.Remove(s.file(id))
}

func (s *JSONStore) List() ([]*Conversation, error) {
	ents, err := os.ReadDir(s.liveDir())
	if err != nil {
		return nil, err
	}
	var out []*Conversation
	for _, e := range ents {
		if len(e.Name()) < 5 || e.Name()[:5] != "conv-" || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[5 : len(e.Name())-5]
		if c, err := s.Get(id); err == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}
