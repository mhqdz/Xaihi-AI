package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
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

	// 保存
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("PUT", "/api/personas/xaihi", `{"body":"你是赛希。"}`))
	if rec.Code != 200 {
		t.Fatalf("保存失败: %d %s", rec.Code, rec.Body.String())
	}

	// 读回
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonReq("GET", "/api/personas/xaihi", ""))
	var got struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Body != "你是赛希。" {
		t.Fatalf("读回 = %d %s", rec.Code, rec.Body.String())
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
	if !strings.Contains(rec.Body.String(), "xaihi") {
		t.Errorf("列表里没有: %s", rec.Body.String())
	}
}
