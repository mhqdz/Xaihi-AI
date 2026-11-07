package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"agentd/internal/config"
	"agentd/internal/llm"
	"agentd/internal/logx"
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

// POST /api/endpoints/probe —— 用**还没保存**的地址和凭据试一次上游 /models。
//
// 新建接口时字段刚填完就能知道"通不通、有没有模型、缺不缺 key"，不用先存再拉：
// 存完才发现 key 是错的，那个接口就白存一条、还得再删。
// 和 GET /api/endpoints/{name}/models 的分工：这个走草稿（不落盘），
// 那个走已保存的接口（「更新信息」按钮）。
func (s *Server) handleEndpointProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	var in struct {
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(in.BaseURL) == "" {
		writeJSON(w, 400, map[string]string{"error": "接口地址为空"})
		return
	}
	found, err := llm.ListModels(in.BaseURL, in.APIKey)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error(), "models": []llm.ModelInfo{}, "count": 0})
		return
	}
	writeJSON(w, 200, map[string]any{"models": found, "count": len(found)})
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
		// 强制问一次上游（这就是"刷新"）：成功写进能力缓存，界面拿到的是带能力的完整列表。
		found, err := llm.ListModels(e.BaseURL, e.APIKey)
		if err == nil {
			var models []config.CachedModel
			for _, m := range found {
				models = append(models, config.CachedModel{
					ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow,
					MaxTokens: m.MaxOutput, Input: m.Input, EffortLevels: m.EffortLevels,
				})
			}
			if err := config.SaveModelCache(name, models); err != nil {
				logx.Warn("写模型能力缓存失败（%s）：%v", name, err)
			}
			writeJSON(w, 200, map[string]any{"models": models, "at": time.Now(), "stale": false})
			return
		}
		// 拉不到：把上次拿到的给界面看，附上时间与原因 —— 硬失败没意义，
		// 下拉里还能继续选之前见过的模型
		if entry, ok := config.ModelCacheEntry(name); ok {
			writeJSON(w, 502, map[string]any{
				"error": err.Error(), "models": entry.Models, "at": entry.At, "stale": true,
			})
			return
		}
		writeJSON(w, 502, map[string]any{
			"error": err.Error(), "models": []config.CachedModel{}, "stale": true,
		})
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

// WarmModelCaches：启动时把过期的模型能力缓存补一次。
//
// 为什么要有：能力缓存只在"有人打开下拉 / 点刷新"时才更新，长期不开面板的配置
// 会一直用一份很旧的数字。这里在启动后台补一次（只补有配置在用的接口、
// 且只补超过 maxAge 的），不阻塞启动，失败只记日志。
func (s *Server) WarmModelCaches(maxAge time.Duration) {
	for _, name := range config.StaleCacheEndpoints(maxAge) {
		e, ok := config.GetEndpoint(name)
		if !ok {
			continue
		}
		found, err := llm.ListModels(e.BaseURL, e.APIKey)
		if err != nil {
			logx.Info("能力缓存补充失败（%s，下次下拉再试）：%v", name, err)
			continue
		}
		var models []config.CachedModel
		for _, m := range found {
			models = append(models, config.CachedModel{
				ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow,
				MaxTokens: m.MaxOutput, Input: m.Input, EffortLevels: m.EffortLevels,
			})
		}
		if err := config.SaveModelCache(name, models); err != nil {
			logx.Warn("写模型能力缓存失败（%s）：%v", name, err)
			continue
		}
		logx.Info("能力缓存已补充（%s）：%d 个模型", name, len(models))
	}
}
