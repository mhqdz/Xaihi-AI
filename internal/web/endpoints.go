package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentd/internal/config"
	"agentd/internal/llm"
)

// 接口（endpoint）= 地址 + 凭据 + 协议；模型（profile）只声明用哪个接口。
//
// 拆开的理由：同一个本地服务常被好几个 profile 共用（不同思考强度、不同 system），
// 以前每个都存一份地址，改一次要改三处。关系对齐"工具 → 模型"。
func (s *Server) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, config.LoadEndpoints())
	case http.MethodPost, http.MethodPut:
		var e config.Endpoint
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := config.UpsertEndpoint(e); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "endpoint": e.Name})
	default:
		writeJSON(w, 405, map[string]string{"error": "GET/POST/PUT only"})
	}
}

func (s *Server) handleEndpointItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/endpoints/")
	// /api/endpoints/<name>/models —— 拉这个接口下有哪些模型
	if strings.HasSuffix(rest, "/models") {
		name := strings.TrimSuffix(rest, "/models")
		e, ok := config.GetEndpoint(name)
		if !ok {
			writeJSON(w, 404, map[string]string{"error": "没有这个接口"})
			return
		}
		if r.Method != http.MethodGet {
			writeJSON(w, 405, map[string]string{"error": "GET only"})
			return
		}
		names, err := llm.ListModels(e.BaseURL, e.APIKey)
		if err != nil {
			writeJSON(w, 502, map[string]any{"error": "取模型列表失败：" + err.Error(), "models": []string{}})
			return
		}
		writeJSON(w, 200, map[string]any{"models": names})
		return
	}
	name := rest
	switch r.Method {
	case http.MethodGet:
		if e, ok := config.GetEndpoint(name); ok {
			writeJSON(w, 200, e)
			return
		}
		writeJSON(w, 404, map[string]string{"error": "没有这个接口"})
	case http.MethodDelete:
		if err := config.DeleteEndpoint(name); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 405, map[string]string{"error": "GET/DELETE only"})
	}
}

// /api/sources —— 来源模板（新建接口时选它来预填地址和协议）。
// 数据来自 sources.json，用户加一条就多一个来源，不用改代码。
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, config.LoadSources())
	case http.MethodPut:
		var list []config.Source
		if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := config.SaveSources(list); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "count": len(list)})
	default:
		writeJSON(w, 405, map[string]string{"error": "GET/PUT only"})
	}
}
