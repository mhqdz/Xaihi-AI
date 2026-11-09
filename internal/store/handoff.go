package store

import (
	"database/sql"
	"time"
)

// 交接相关表：任务、步骤、待投递消息（outbox）
type Task struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	ParentConv int64  `json:"parent_conv"`
	ChildConv  int64  `json:"child_conv"`
	CardPath   string `json:"card_path"`
	Status     string `json:"status"` // running|review|done|failed|interrupted|cancelled
	Error      string `json:"error"`
	CreatedAt  string `json:"created_at"`
	FinishedAt string `json:"finished_at"`
}

type TaskStep struct {
	Seq      int    `json:"seq"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // run|spawn|restore|artifact
	Cmd      string `json:"cmd"`
	ExitCode int    `json:"exit_code"`
	OutChars int    `json:"out_chars"`
	Ms       int64  `json:"ms"`
	Status   string `json:"status"` // ok|failed|skipped
	Output   string `json:"output"`
}

type Outbox struct {
	ID          int64  `json:"id"`
	ConvID      int64  `json:"conv_id"`
	Origin      string `json:"origin"`
	Status      string `json:"status"` // pending|delivered|failed
	Text        string `json:"text"`
	CreatedAt   string `json:"created_at"`
	DeliveredAt string `json:"delivered_at"`
}

func migrateHandoff(db *sql.DB) error {
	stmts := []string{
		`ALTER TABLE conversations ADD COLUMN parent_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE conversations ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat'`,
		`ALTER TABLE messages ADD COLUMN origin TEXT NOT NULL DEFAULT 'user'`,
		`CREATE TABLE IF NOT EXISTS tasks(
			id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL,
			parent_conv INTEGER NOT NULL, child_conv INTEGER NOT NULL DEFAULT 0,
			card_path TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'running',
			error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS task_steps(
			id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER NOT NULL, seq INTEGER NOT NULL,
			name TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT 'run', cmd TEXT NOT NULL DEFAULT '',
			exit_code INTEGER NOT NULL DEFAULT 0, out_chars INTEGER NOT NULL DEFAULT 0,
			ms INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'ok', output TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS outbox(
			id INTEGER PRIMARY KEY AUTOINCREMENT, conv_id INTEGER NOT NULL,
			origin TEXT NOT NULL DEFAULT 'system', status TEXT NOT NULL DEFAULT 'pending',
			text TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, delivered_at TEXT NOT NULL DEFAULT '')`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			// ALTER 重复执行会报错，忽略；CREATE 的错误要暴露
			if len(q) > 6 && q[:6] == "ALTER " {
				continue
			}
			return err
		}
	}
	return nil
}

func now() string { return time.Now().Format(time.RFC3339Nano) }

func (s *SQLiteStore) CreateTask(kind string, parent int64, cardPath string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO tasks(kind,parent_conv,card_path,status,created_at) VALUES(?,?,?,'running',?)`,
		kind, parent, cardPath, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLiteStore) SetTaskChild(id, child int64) error {
	_, err := s.db.Exec(`UPDATE tasks SET child_conv=? WHERE id=?`, child, id)
	return err
}

func (s *SQLiteStore) FinishTask(id int64, status, errMsg string) error {
	_, err := s.db.Exec(`UPDATE tasks SET status=?,error=?,finished_at=? WHERE id=?`, status, errMsg, now(), id)
	return err
}

func (s *SQLiteStore) AddStep(taskID int64, st TaskStep) error {
	_, err := s.db.Exec(`INSERT INTO task_steps(task_id,seq,name,kind,cmd,exit_code,out_chars,ms,status,output)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, taskID, st.Seq, st.Name, st.Kind, st.Cmd, st.ExitCode, st.OutChars, st.Ms, st.Status, st.Output)
	return err
}

func (s *SQLiteStore) Steps(taskID int64) ([]TaskStep, error) {
	rows, err := s.db.Query(`SELECT seq,name,kind,cmd,exit_code,out_chars,ms,status,output FROM task_steps WHERE task_id=? ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskStep
	for rows.Next() {
		var t TaskStep
		if err := rows.Scan(&t.Seq, &t.Name, &t.Kind, &t.Cmd, &t.ExitCode, &t.OutChars, &t.Ms, &t.Status, &t.Output); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *SQLiteStore) GetTask(id int64) (*Task, error) {
	var t Task
	err := s.db.QueryRow(`SELECT id,kind,parent_conv,child_conv,card_path,status,error,created_at,finished_at FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.Kind, &t.ParentConv, &t.ChildConv, &t.CardPath, &t.Status, &t.Error, &t.CreatedAt, &t.FinishedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *SQLiteStore) ListTasks() ([]*Task, error) {
	rows, err := s.db.Query(`SELECT id,kind,parent_conv,child_conv,card_path,status,error,created_at,finished_at FROM tasks ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Kind, &t.ParentConv, &t.ChildConv, &t.CardPath, &t.Status, &t.Error, &t.CreatedAt, &t.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, nil
}

func (s *SQLiteStore) MarkTasksInterrupted() (int, error) {
	res, err := s.db.Exec(`UPDATE tasks SET status='interrupted',error='xaihi 重启中断' WHERE status='running'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) CreateOutbox(conv int64, origin, text string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO outbox(conv_id,origin,status,text,created_at) VALUES(?,?,'pending',?,?)`, conv, origin, text, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLiteStore) OutboxItem(id int64) (*Outbox, error) {
	var o Outbox
	err := s.db.QueryRow(`SELECT id,conv_id,origin,status,text,created_at,delivered_at FROM outbox WHERE id=?`, id).
		Scan(&o.ID, &o.ConvID, &o.Origin, &o.Status, &o.Text, &o.CreatedAt, &o.DeliveredAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *SQLiteStore) PendingOutbox() ([]*Outbox, error) {
	rows, err := s.db.Query(`SELECT id,conv_id,origin,status,text,created_at,delivered_at FROM outbox WHERE status='pending' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Outbox
	for rows.Next() {
		var o Outbox
		if err := rows.Scan(&o.ID, &o.ConvID, &o.Origin, &o.Status, &o.Text, &o.CreatedAt, &o.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, &o)
	}
	return out, nil
}

func (s *SQLiteStore) OutboxForConv(conv int64, limit int) ([]*Outbox, error) {
	rows, err := s.db.Query(`SELECT id,conv_id,origin,status,text,created_at,delivered_at FROM outbox WHERE conv_id=? ORDER BY id DESC LIMIT ?`, conv, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Outbox
	for rows.Next() {
		var o Outbox
		if err := rows.Scan(&o.ID, &o.ConvID, &o.Origin, &o.Status, &o.Text, &o.CreatedAt, &o.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, &o)
	}
	return out, nil
}

func (s *SQLiteStore) SetOutboxStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE outbox SET status=?, delivered_at=? WHERE id=?`, status, now(), id)
	return err
}

// ParentOf：父会话 id（0 = 顶层）
func (s *SQLiteStore) ParentOf(conv int64) int64 {
	var p int64
	_ = s.db.QueryRow(`SELECT parent_id FROM conversations WHERE id=?`, conv).Scan(&p)
	return p
}

// RestoreArchived：把归档会话恢复（outbox 投递到已归档父会话时用）
func (s *SQLiteStore) RestoreArchived(id int64) {
	_, _ = s.db.Exec(`UPDATE conversations SET archived=0 WHERE id=?`, id)
}
