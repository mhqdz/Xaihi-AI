package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentd/internal/config"
	"agentd/internal/llm"
)

// 模型（第三层的中间）：属于某个接口，是"模型事实"（id / 窗口 / 模态）。
// profile 引用它来配置"怎么用"（提示 / 工具 / 默认强度）。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, config.LoadModels())
	case http.MethodPost, http.MethodPut:
		var m config.Model
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := config.UpsertModel(m); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "name": config.ModelName(m.Endpoint, m.ID)})
	default:
		writeJSON(w, 405, map[string]string{"error": "GET/POST/PUT only"})
	}
}

func (s *Server) handleModelItem(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/models/")
	switch r.Method {
	case http.MethodGet:
		if m, ok := config.GetModel(name); ok {
			writeJSON(w, 200, m)
			return
		}
		writeJSON(w, 404, map[string]string{"error": "没有这个模型"})
	case http.MethodDelete:
		if err := config.DeleteModel(name); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeJSON(w, 405, map[string]string{"error": "GET/DELETE only"})
	}
}

// handleEndpointModels 拉某个接口下的模型，用于"发现"：
// 返回的是上游 /models 的原始列表，再加上"哪些已经在 models.json 里注册过"。
func (s *Server) handleEndpointDiscover(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/discover/")
	e, ok := config.GetEndpoint(name)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "没有这个接口"})
		return
	}
	ids, err := llm.ListModels(e.BaseURL, e.APIKey)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": "取模型列表失败：" + err.Error(), "models": []string{}})
		return
	}
	registered := map[string]bool{}
	for _, m := range config.LoadModels() {
		if m.Endpoint == name {
			registered[m.ID] = true
		}
	}
	// 把上游给的能力一起带回去：界面登记时可以直接沿用，不用手填窗口和模态
	type entry struct {
		ID            string   `json:"id"`
		Registered    bool     `json:"registered"`
		Name          string   `json:"name,omitempty"`
		ContextWindow int      `json:"context_window,omitempty"`
		MaxTokens     int      `json:"max_tokens,omitempty"`
		Input         []string `json:"input,omitempty"`
		EffortLevels  []string `json:"effort_levels,omitempty"`
		EffortDefault string   `json:"effort_default,omitempty"`
	}
	out := make([]entry, 0, len(ids))
	for _, m := range ids {
		out = append(out, entry{ID: m.ID, Registered: registered[m.ID], Name: m.Name,
			ContextWindow: m.ContextWindow, MaxTokens: m.MaxOutput, Input: m.Input,
			EffortLevels: m.EffortLevels, EffortDefault: m.EffortDefault})
	}
	writeJSON(w, 200, map[string]any{"endpoint": name, "models": out})
}
