package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentd/internal/agent"
	"agentd/internal/config"
	"agentd/internal/store"
)

// fakeUpstream：一个假模型。第一轮返回 bash 的 tool_call，第二轮返回最终文本。
func fakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	round := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		hasTool := false
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasTool = true
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		if !hasTool && round == 0 {
			round++
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"echo hi\\\"}\"}}]}}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"工具说 hi\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
}

func newTestServer(t *testing.T) (*Server, *store.JSONStore, *config.Config) {
	t.Helper()
	up := fakeUpstream(t)
	t.Cleanup(up.Close)
	cfg := config.Default()
	cfg.BaseURL = up.URL
	cfg.Model = "fake"
	cfg.DataDir = t.TempDir()
	st, err := store.NewJSON(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	prof := config.Profile{Name: "test", BaseURL: up.URL, Model: "fake", Tools: []string{"bash"}}
	a := agent.New(cfg, st, prof)
	return New(cfg, a, st), st, &cfg
}

// jsonReq：写请求统一走这里。requireJSONBody 中间件要求带 body 的 PUT/POST
// 声明 application/json（挡 CSRF 的简单请求），真实客户端都带，测试也得带。
func jsonReq(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestHealth(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	}
}

func TestChatSSEWithToolLoop(t *testing.T) {
	s, st, _ := newTestServer(t)
	req := jsonReq("POST", "/api/chat", `{"text":"跑一下 echo hi","profile":"test"}`)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"event: meta", "event: tool", "event: delta", "event: done"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE 缺 %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "工具说 hi") {
		t.Fatalf("最终回答缺失:\n%s", body)
	}
	// 会话应已落盘：user + assistant(tool_calls) + tool + assistant
	list, _ := st.List()
	if len(list) != 1 {
		t.Fatalf("应有一个会话，得到 %d", len(list))
	}
	roles := []string{}
	for _, m := range list[0].Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,assistant,tool,assistant" {
		t.Fatalf("消息序列不对: %v", roles)
	}
}

func TestConvsAndConvDetail(t *testing.T) {
	s, st, _ := newTestServer(t)
	c := &store.Conversation{ID: "c1", Title: "标题"}
	c.Messages = append(c.Messages, store.Message{Role: "user", Content: "hi"})
	_ = st.Save(c)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/convs", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "c1") {
		t.Fatalf("convs: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/conv/c1", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "标题") {
		t.Fatalf("conv detail: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/conv/nope", nil))
	if rec.Code != 404 {
		t.Fatalf("不存在的会话应 404，得到 %d", rec.Code)
	}
}

func TestProfilesCRUD(t *testing.T) {
	s, _, _ := newTestServer(t)
	_ = t.TempDir()
	// 用一个临时 profiles 文件隔离
	t.Setenv("AGENTD_PROFILES", t.TempDir()+"/profiles.json")

	body := `{"name":"p1","base_url":"http://x","model":"m"}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("POST", "/api/profiles", body))
	if rec.Code != 200 {
		t.Fatalf("创建: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/profiles/p1", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "p1") {
		t.Fatalf("读取: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/profiles/p1", nil))
	if rec.Code != 200 {
		t.Fatalf("删除: %d %s", rec.Code, rec.Body.String())
	}
	// 非法 effort 应 400
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("POST", "/api/profiles",
		`{"name":"bad","base_url":"u","model":"m","reasoning_effort":"ultra"}`))
	if rec.Code != 400 {
		t.Fatalf("非法 effort 应 400，得到 %d", rec.Code)
	}
}

func TestChatStreamIsIncremental(t *testing.T) {
	s, _, _ := newTestServer(t)
	// 用真实 listener 验证"边生成边到"，而不是一次性吐出
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/chat", strings.NewReader(`{"text":"hi","profile":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	gotEvent := false
	deadline := time.Now().Add(30 * time.Second)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: ") {
			gotEvent = true
			break
		}
		if time.Now().After(deadline) {
			break
		}
	}
	if !gotEvent {
		t.Fatal("没收到任何 SSE 事件")
	}
}
