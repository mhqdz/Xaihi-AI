package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"agentd/internal/llm"
)

// SQLiteStore：一个会话一行，消息一列一行（消息只追加，不二次编辑）。
type SQLiteStore struct{ db *sql.DB }

func ensureReasoningMs(db *sql.DB) {
	db.Exec(`ALTER TABLE messages ADD COLUMN reasoning_ms INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE messages ADD COLUMN tool_ms INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE messages ADD COLUMN status TEXT NOT NULL DEFAULT ''`)
}

func NewSQLite(dir string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "agentd.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS conversations(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  summary_upto INTEGER NOT NULL DEFAULT 0,
  summary_model TEXT NOT NULL DEFAULT '',
  summary_at TEXT,
  kv TEXT NOT NULL DEFAULT '{}',
  archived INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  seq INTEGER NOT NULL DEFAULT 0,
  conv_id INTEGER NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  reasoning TEXT NOT NULL DEFAULT '',
  reasoning_ms INTEGER NOT NULL DEFAULT 0,
  tool_calls TEXT NOT NULL DEFAULT '',
  tool_call_id TEXT NOT NULL DEFAULT '',
  tool_result TEXT NOT NULL DEFAULT '',
  images TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS idx_msg_conv_seq ON messages(conv_id, seq);
`)
	if err != nil {
		return nil, err
	}
	ensureReasoningMs(db)
	if err := migrateHandoff(db); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) NextID() string { return "" } // 由 AUTOINCREMENT 负责

func (s *SQLiteStore) Get(id string) (*Conversation, error) {
	// 必须严格解析：fmt.Sscanf("%d") 会忽略尾部垃圾，于是 /api/conv/51abc
	// 被当成 51，把别人的会话原样返回（200）。strconv 对 "51abc" 直接报错。
	n, perr := strconv.Atoi(strings.TrimSpace(id))
	if perr != nil || n <= 0 {
		return nil, os.ErrNotExist
	}
	var c Conversation
	var kv, sat, cat, uat string
	var kind string
	err := s.db.QueryRow(`SELECT id,title,summary,summary_upto,summary_model,COALESCE(summary_at,''),kv,created_at,updated_at,parent_id,kind
		FROM conversations WHERE id=? AND archived=0`, n).
		Scan(&n, &c.Title, &c.Summary, &c.SummaryUpto, &c.SummaryModel, &sat, &kv, &cat, &uat, &c.ParentID, &kind)
	c.Kind = kind
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, cat)
	c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, uat)
	if err != nil {
		return nil, os.ErrNotExist
	}
	c.ID = fmt.Sprint(n)
	_ = json.Unmarshal([]byte(kv), &c.KV)
	if sat != "" {
		if t, err := time.Parse(time.RFC3339Nano, sat); err == nil {
			c.SummaryAt = t
		}
	}
	ms, err := s.messages(n)
	if err != nil {
		return nil, err
	}
	c.Messages = ms
	return &c, nil
}

func (s *SQLiteStore) messages(convID int) ([]Message, error) {
	rows, err := s.db.Query(`SELECT seq,role,content,reasoning,reasoning_ms,tool_calls,tool_call_id,tool_result,images,created_at,origin,tool_ms,status
		FROM messages WHERE conv_id=? ORDER BY seq`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var tc, tr, im, ca string
		var seq int
		if err := rows.Scan(&seq, &m.Role, &m.Content, &m.Reasoning, &m.ReasoningMs, &tc, &m.ToolCallID, &tr, &im, &ca, &m.Origin, &m.ToolMs, &m.Status); err != nil {
			return nil, err
		}
		m.Seq = seq
		if tc != "" {
			_ = json.Unmarshal([]byte(tc), &m.ToolCalls)
		}
		if tr != "" {
			var v any
			if json.Unmarshal([]byte(tr), &v) == nil {
				m.ToolResult = v
			}
		}
		if im != "" {
			_ = json.Unmarshal([]byte(im), &m.Images)
		}
		if t, err := time.Parse(time.RFC3339Nano, ca); err == nil {
			m.CreatedAt = t
		}
		out = append(out, m)
	}
	return out, nil
}

// InsertPending 预分配一条消息行（status=running），返回 seq。
// 流式输出期间靠它“先占位后填充”：进程崩了至少留下一条 failed 消息，而不是什么都没有。
func (s *SQLiteStore) InsertPending(convID int64, role string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var nextSeq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE conv_id=?`, convID).Scan(&nextSeq); err != nil {
		return 0, err
	}
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO messages(conv_id,seq,role,content,reasoning,reasoning_ms,tool_calls,tool_call_id,tool_result,images,created_at,origin,tool_ms,status)
		VALUES(?,?,?,'','',0,'','','','',?,'',0,'running')`, convID, nextSeq, role, now); err != nil {
		return 0, err
	}
	return nextSeq, tx.Commit()
}

// UpdateMessage 流式/收尾时更新一条已分配 seq 的消息（content/reasoning/status）。
func (s *SQLiteStore) UpdateMessage(convID int64, seq int, content, reasoning string, reasoningMs int64, status string) error {
	_, err := s.db.Exec(`UPDATE messages SET content=?, reasoning=?, reasoning_ms=?, status=? WHERE conv_id=? AND seq=?`,
		content, reasoning, reasoningMs, status, convID, seq)
	return err
}

// DeleteMessage 删掉一条按 seq 定位的消息（工具轮放弃流式占位行时用）。
func (s *SQLiteStore) DeleteMessage(convID int64, seq int) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE conv_id=? AND seq=?`, convID, seq)
	return err
}

// MarkRunningFailed 启动时把上次进程留下的 running 消息标记为 failed。
// systemd 单实例：服务启动时旧进程必然已退出，不存在“还活着但在 running”的情况。
func (s *SQLiteStore) MarkRunningFailed() (int64, error) {
	res, err := s.db.Exec(`UPDATE messages SET status='failed' WHERE status='running'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) Save(c *Conversation) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	nStr := now.Format(time.RFC3339Nano)
	kv, _ := json.Marshal(c.KV)
	var sat any
	if !c.SummaryAt.IsZero() {
		sat = c.SummaryAt.Format(time.RFC3339Nano)
	}
	if c.ID == "" {
		res, err := tx.Exec(`INSERT INTO conversations(title,summary,summary_upto,summary_model,summary_at,kv,created_at,updated_at,parent_id,kind)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, c.Title, c.Summary, c.SummaryUpto, c.SummaryModel, sat, string(kv), nStr, nStr, c.ParentID, c.Kind)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		c.ID = fmt.Sprint(id)
	} else {
		if _, err := tx.Exec(`UPDATE conversations SET title=?,summary=?,summary_upto=?,summary_model=?,summary_at=?,kv=?,updated_at=?,parent_id=?,kind=? WHERE id=?`,
			c.Title, c.Summary, c.SummaryUpto, c.SummaryModel, sat, string(kv), nStr, c.ParentID, c.Kind, c.ID); err != nil {
			return err
		}
	}
	// 只追加新消息（按 Seq 是否存在判断，绝不改历史 ✓）
	for i := range c.Messages {
		m := &c.Messages[i]
		if m.Seq != 0 {
			continue
		}
		tc, _ := json.Marshal(m.ToolCalls)
		tr := ""
		if m.ToolResult != nil {
			b, _ := json.Marshal(m.ToolResult)
			tr = string(b)
		}
		im, _ := json.Marshal(m.Images)
		var nextSeq int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE conv_id=?`, c.ID).Scan(&nextSeq); err != nil {
			return err
		}
		if m.Status == "" {
			m.Status = "done" // user/tool 与普通回复落库即完整；assistant 的 running/interrupted/failed 由 agent 显式设置
		}
		origin := m.Origin
		if origin == "" {
			origin = "user"
		}
		res, err := tx.Exec(`INSERT INTO messages(conv_id,seq,role,content,reasoning,reasoning_ms,tool_calls,tool_call_id,tool_result,images,created_at,origin,tool_ms,status)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, c.ID, nextSeq, m.Role, m.Content, m.Reasoning, m.ReasoningMs, string(tc), m.ToolCallID, tr, string(im), nStr, origin, m.ToolMs, m.Status)
		if err != nil {
			return err
		}
		_ = res
		m.Seq = nextSeq // 会话内序号，回填后避免重复插入 ✓
	}
	c.UpdatedAt = now
	return tx.Commit()
}

func (s *SQLiteStore) list(archived int) ([]*Conversation, error) {
	rows, err := s.db.Query(`SELECT c.id,c.title,c.created_at,c.updated_at,(SELECT COUNT(*) FROM messages m WHERE m.conv_id=c.id),c.parent_id,c.kind
		FROM conversations c WHERE archived=? ORDER BY updated_at DESC`, archived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Conversation
	for rows.Next() {
		var c Conversation
		var id int
		var cat, uat string
		if err := rows.Scan(&id, &c.Title, &cat, &uat, &c.MsgCount, &c.ParentID, &c.Kind); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339Nano, cat)
		c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, uat)
		c.ID = fmt.Sprint(id)
		out = append(out, &c)
	}
	return out, nil
}

func (s *SQLiteStore) List() ([]*Conversation, error)         { return s.list(0) }
func (s *SQLiteStore) ListArchived() ([]*Conversation, error) { return s.list(1) }

// ArchiveInactiveBefore 把 updated_at 早于 t 的会话批量标记为归档，返回影响条数
func (s *SQLiteStore) ArchiveInactiveBefore(t time.Time) (int, error) {
	res, err := s.db.Exec(`UPDATE conversations SET archived=1 WHERE archived=0 AND updated_at < ?`,
		t.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) Archive(id string) error {
	res, err := s.db.Exec(`UPDATE conversations SET archived=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	// 影响 0 行说明这个 id 根本不存在。不返回错误的话，调用方会回 ok:true，
	// 等于告诉客户端「归档成功了」，实际什么也没发生。
	if n, _ := res.RowsAffected(); n == 0 {
		return os.ErrNotExist
	}
	return nil
}

func (s *SQLiteStore) Restore(id string) error {
	res, err := s.db.Exec(`UPDATE conversations SET archived=0 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return os.ErrNotExist
	}
	return nil
}

func (s *SQLiteStore) Delete(id string, hard bool) error {
	if hard {
		if _, err := s.db.Exec(`DELETE FROM messages WHERE conv_id=?`, id); err != nil {
			return err
		}
		res, err := s.db.Exec(`DELETE FROM conversations WHERE id=?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return os.ErrNotExist
		}
		return nil
	}
	return s.Archive(id)
}

var _ Store = (*SQLiteStore)(nil)
var _ = llm.ToolCall{}
