package web

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"agentd/internal/agent"
	"agentd/internal/config"
	"agentd/internal/store"
)

func newHandoffServer(t *testing.T) (*Server, *store.SQLiteStore) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.NewSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: dir, BaseURL: "http://127.0.0.1:1/v1", Model: "m"}
	prof := config.Profile{Name: "test", BaseURL: cfg.BaseURL, Model: "m"}
	a := agent.New(cfg, st, prof)
	s := New(cfg, a, st)
	if err := s.InitHandoff(dir); err != nil {
		t.Fatal(err)
	}
	return s, st
}

func TestWakeAuth(t *testing.T) {
	s, st := newHandoffServer(t)
	c := &store.Conversation{Title: "p"}
	if err := st.Save(c); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	body, _ := json.Marshal(map[string]string{"conv_id": c.ID, "text": "后台任务完成"})

	req := jsonReq("POST", "/api/wake", string(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("没有 token 应该 401，得到 %d", w.Code)
	}

	tok := s.wakeToken()
	req = jsonReq("POST", "/api/wake", string(body))
	req.Header.Set("X-Agentd-Token", tok)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("带 token 应该 200，得到 %d: %s", w.Code, w.Body.String())
	}
	obs, _ := st.OutboxForConv(1, 10)
	if len(obs) != 1 || obs[0].Status != "pending" || obs[0].Origin != "system" {
		t.Fatalf("outbox 不对: %+v", obs)
	}
}

func TestTasksEndpoint(t *testing.T) {
	s, st := newHandoffServer(t)
	c := &store.Conversation{Title: "p"}
	_ = st.Save(c)
	tid, _ := st.CreateTask("k", 1, "")
	_ = st.AddStep(tid, store.TaskStep{Seq: 1, Kind: "run", Cmd: "echo hi", Status: "ok"})
	h := s.Handler()
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("列表 200? %d", w.Code)
	}
	var out struct {
		Tasks []store.Task `json:"tasks"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Tasks) != 1 || out.Tasks[0].Kind != "k" {
		t.Fatalf("任务列表不对: %s", w.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/tasks/"+toStr(tid), nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("echo hi")) {
		t.Fatalf("详情不对: %d %s", w.Code, w.Body.String())
	}
}

func TestWaketokenFile(t *testing.T) {
	s, _ := newHandoffServer(t)
	tok := s.wakeToken()
	if len(tok) < 8 {
		t.Fatalf("token 太短: %q", tok)
	}
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".data", "agentd", "wake.token"))
	if err != nil || len(b) == 0 {
		t.Fatalf("token 文件没写: %v", err)
	}
}

func toStr(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
