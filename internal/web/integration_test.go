package web_test

// 集成测试：真实 HTTP 服务（httptest.Server）+ 假上游模型，跑完整链路。
// 需要真实模型时：AGENTD_E2E=1 AGENTD_E2E_UPSTREAM=http://127.0.0.1:18320/180/v1 go test -run TestE2ERealModel ./internal/web/

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"agentd/internal/agent"
	"agentd/internal/config"
	"agentd/internal/store"
	"agentd/internal/web"
)

func setup(t *testing.T, upstream string) (*httptest.Server, *store.JSONStore, *config.Config, func()) {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.BaseURL = upstream
	cfg.Model = "fake"
	t.Setenv("AGENTD_PROFILES", t.TempDir()+"/profiles.json")
	st, err := store.NewJSON(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	prof := config.Profile{Name: "test", BaseURL: upstream, Model: "fake", Tools: []string{"bash", "recall"}}
	a := agent.New(cfg, st, prof)
	srv := httptest.NewServer(web.New(cfg, a, st).Handler())
	return srv, st, &cfg, srv.Close
}

// 假上游：第一轮发 tool_call，第二轮给最终文本
func fakeUpstream() *httptest.Server {
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f, _ := w.(http.Flusher)
		if calls == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"echo 集成测试\\\"}\"}}]}}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"完成：集成测试\"}}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if f != nil {
			f.Flush()
		}
	}))
}

func TestIntegrationFullFlow(t *testing.T) {
	up := fakeUpstream()
	defer up.Close()
	srv, st, _, done := setup(t, up.URL)
	defer done()

	// ① health
	var h map[string]any
	getJSON(t, srv.URL+"/api/health", &h)
	if h["ok"] != true {
		t.Fatalf("health: %v", h)
	}

	// ② 建 profile + 读回 + 列表
	postJSON(t, srv.URL+"/api/profiles", `{"name":"it","base_url":"`+up.URL+`","model":"fake","tools":["bash"],"context_window":8192,"input":["text"]}`)
	var p config.Profile
	getJSON(t, srv.URL+"/api/profiles/it", &p)
	if p.Model != "fake" || len(p.Tools) != 1 {
		t.Fatalf("profile 读回不对: %+v", p)
	}

	// ③ 聊天（SSE，含工具闭环）
	convID, events := streamChat(t, srv.URL, `{"text":"跑一下 echo 集成测试","profile":"it"}`)
	if convID == "" {
		t.Fatal("没有拿到 conv_id")
	}
	for _, want := range []string{"meta", "tool", "delta", "done"} {
		if events[want] == 0 {
			t.Fatalf("缺事件 %s（收到: %v）", want, events)
		}
	}

	// ④ 会话列表 + 详情
	var convs []struct{ ID, Title string }
	getJSON(t, srv.URL+"/api/convs", &convs)
	if len(convs) != 1 || convs[0].ID != convID {
		t.Fatalf("convs: %+v", convs)
	}
	var c store.Conversation
	getJSON(t, srv.URL+"/api/conv/"+convID, &c)
	if len(c.Messages) < 3 || c.Messages[0].Role != "user" {
		t.Fatalf("会话详情不对: %+v", c.Messages)
	}

	// ⑤ 归档 → 活跃列表空 / 归档列表有 → 详情仍可读
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/conv/"+convID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("归档失败: %v %v", err, resp)
	}
	resp.Body.Close()
	var live []struct{ ID string }
	getJSON(t, srv.URL+"/api/convs", &live)
	if len(live) != 0 {
		t.Fatalf("归档后活跃应为空: %+v", live)
	}
	var arch []struct{ ID string }
	getJSON(t, srv.URL+"/api/convs?archived=1", &arch)
	if len(arch) != 1 || arch[0].ID != convID {
		t.Fatalf("归档列表不对: %+v", arch)
	}

	// ⑥ 第二段对话（新会话），确认两段互不污染
	conv2, _ := streamChat(t, srv.URL, `{"text":"再说一次","profile":"it"}`)
	if conv2 == "" || conv2 == convID {
		t.Fatalf("第二个会话 id 异常: %q vs %q", conv2, convID)
	}
	if l, _ := st.List(); len(l) != 1 {
		t.Fatalf("活跃会话应只有 1 个，得到 %d", len(l))
	}
	if a, _ := st.ListArchived(); len(a) != 1 {
		t.Fatalf("归档应 1 个，得到 %d", len(a))
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s → %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, url, body string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s → %d", url, resp.StatusCode)
	}
}

func streamChat(t *testing.T, base, body string) (string, map[string]int) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("chat → %d", resp.StatusCode)
	}
	events := map[string]int{}
	convID := ""
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	var ev string
	deadline := time.Now().Add(60 * time.Second)
	for sc.Scan() {
		line := sc.Text()
		if time.Now().After(deadline) {
			t.Fatal("SSE 超时")
		}
		if strings.HasPrefix(line, "event: ") {
			ev = line[7:]
			events[ev]++
		} else if strings.HasPrefix(line, "data: ") && ev == "meta" {
			var m map[string]string
			if json.Unmarshal([]byte(line[6:]), &m) == nil {
				convID = m["conv_id"]
			}
		}
	}
	return convID, events
}

// 需要真实模型时手动跑：AGENTD_E2E=1 AGENTD_E2E_UPSTREAM=… go test -run TestE2ERealModel
func TestE2ERealModel(t *testing.T) {
	if os.Getenv("AGENTD_E2E") == "" {
		t.Skip("设置 AGENTD_E2E=1 才跑真实模型")
	}
	up := os.Getenv("AGENTD_E2E_UPSTREAM")
	if up == "" {
		up = "http://127.0.0.1:18320/180/v1"
	}
	srv, _, _, done := setup(t, up)
	defer done()
	convID, events := streamChat(t, srv.URL, `{"text":"只回复两个字：收到","profile":"test"}`)
	if convID == "" || events["done"] == 0 {
		t.Fatalf("真实模型没跑通: conv=%q events=%v", convID, events)
	}
}
