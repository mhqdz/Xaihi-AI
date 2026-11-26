package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"xaihi/internal/config"
)

// 人格的读写接口：存进去能读回来，空内容被拒，不存在的人格 404。
func TestPersonaAPI(t *testing.T) {
	s, _, cfg := newTestServer(t)
	// handler 用的是包级 DataDir，这里跟着切过去
	t.Setenv("XAIHI_DATA_DIR", cfg.DataDir)

	// 列表最初是空的
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("GET", "/api/personas", ""))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("初始列表 = %d %s", rec.Code, rec.Body.String())
	}

	if err := config.UpsertProfile(config.Profile{
		Name: "dreamer", Model: "m", BaseURL: "http://127.0.0.1", ContextWindow: 8192, Input: []string{"text"},
	}); err != nil {
		t.Fatal(err)
	}
	// 保存
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("PUT", "/api/personas/xaihi", `{"body":"你是赛希。","memory":true,"wiki":true,"dreamer":"dreamer"}`))
	if rec.Code != 200 {
		t.Fatalf("保存失败: %d %s", rec.Code, rec.Body.String())
	}

	// 读回
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("GET", "/api/personas/xaihi", ""))
	var got struct {
		Name    string `json:"name"`
		Body    string `json:"body"`
		Memory  bool   `json:"memory"`
		Wiki    bool   `json:"wiki"`
		Dreamer string `json:"dreamer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Body != "你是赛希。" || !got.Memory || !got.Wiki || got.Dreamer != "dreamer" {
		t.Fatalf("读回 = %d %s", rec.Code, rec.Body.String())
	}

	// 旧客户端只提交 body：人格能力配置必须保持不变。
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("PUT", "/api/personas/xaihi", `{"body":"更新正文。"}`))
	if rec.Code != 200 {
		t.Fatalf("只更新正文失败：%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("GET", "/api/personas/xaihi", ""))
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Body != "更新正文。" || !got.Memory || !got.Wiki || got.Dreamer != "dreamer" {
		t.Fatalf("body-only 请求覆盖了人格配置：%s", rec.Body.String())
	}

	// 空内容被拒
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("PUT", "/api/personas/xaihi", `{"body":"   "}`))
	if rec.Code != 400 {
		t.Errorf("空内容应该 400，得到 %d", rec.Code)
	}

	// 不存在的人格
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("GET", "/api/personas/nope", ""))
	if rec.Code != 404 {
		t.Errorf("不存在应该 404，得到 %d", rec.Code)
	}

	// 非法名字（路径穿越）被拒
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("PUT", "/api/personas/..%2Fevil", `{"body":"x"}`))
	if rec.Code == 200 {
		t.Error("非法名字不该成功")
	}

	// 列表里能看到
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("GET", "/api/personas", ""))
	if !strings.Contains(rec.Body.String(), "xaihi") || !strings.Contains(rec.Body.String(), `"memory":true`) || !strings.Contains(rec.Body.String(), `"wiki":true`) {
		t.Errorf("列表里没有人格或能力配置: %s", rec.Body.String())
	}
}

func TestPersonaBodyOnlyDoesNotMaterializeProfile(t *testing.T) {
	s, _, cfg := newTestServer(t)
	t.Setenv("XAIHI_DATA_DIR", cfg.DataDir)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("PUT", "/api/personas/legacy", `{"body":"旧人格正文"}`))
	if rec.Code != 200 {
		t.Fatalf("保存失败：%d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(config.PersonaProfilePath("legacy")); !os.IsNotExist(err) {
		t.Errorf("body-only 不该主动创建人格 JSON：%v", err)
	}
}
