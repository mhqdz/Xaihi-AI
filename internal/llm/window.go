package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// 上下文窗口：从上游 /v1/models 的 meta.n_ctx 读，读不到再用调用方给的默认值。
// 换模型/换引擎时无需改配置 —— 只要对方在 /models 里报了窗口就行。
type windowEntry struct {
	n     int
	at    time.Time
	model string
}

var (
	winMu    sync.Mutex
	winCache = map[string]windowEntry{}
	winTTL   = 10 * time.Minute
)

// ContextWindow 返回 model 的上下文窗口（tokens）。失败返回 0。
func ContextWindow(baseURL, apiKey, model string) int {
	key := strings.TrimRight(baseURL, "/") + "|" + model
	winMu.Lock()
	if e, ok := winCache[key]; ok && time.Since(e.at) < winTTL && e.model == model {
		n := e.n
		winMu.Unlock()
		return n
	}
	winMu.Unlock()

	n := fetchWindow(baseURL, apiKey, model)
	if n > 0 {
		winMu.Lock()
		winCache[key] = windowEntry{n: n, at: time.Now(), model: model}
		winMu.Unlock()
	}
	return n
}

func fetchWindow(baseURL, apiKey, model string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return 0
	}
	req.Close = true
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
			// 云端（OpenAI 兼容）常见：上下文窗口直接放在顶层
			ContextWindow int `json:"context_window"`
			ContextLen    int `json:"context_length"`
			MaxOut        int `json:"max_output_tokens"`
			Meta          struct {
				NCtx       int `json:"n_ctx"`
				ContextLen int `json:"context_length"`
				MaxCtx     int `json:"max_context"`
				NCtxTrain  int `json:"n_ctx_train"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0
	}
	for _, d := range out.Data {
		if model != "" && d.ID != model {
			continue
		}
		if d.ContextWindow > 0 {
			return d.ContextWindow
		}
		if d.ContextLen > 0 {
			return d.ContextLen
		}
		for _, n := range []int{d.Meta.NCtx, d.Meta.ContextLen, d.Meta.MaxCtx, d.Meta.NCtxTrain} {
			if n > 0 {
				return n
			}
		}
	}
	return 0
}

// ModelInfo：上游 /v1/models 能告诉我们的模型能力。
//
// 各家字段命名不统一，这里做一次归一：
//
//	llama.cpp：            meta.n_ctx、architecture.input_modalities
//	DeepSeek / OpenAI 系： context_window、max_output_tokens、input_modalities、effort.*
//	OpenCode / OpenRouter：limit.context、limit.output、modalities.input、capabilities.reasoning.effort
//
// 官方 OpenAI 的 /v1/models 只给 {id, object, owned_by}，什么都不给；
// OpenRouter / OpenCode 之类的聚合器给得最全。所以这里全部是可选的 —— 拿不到就留零值，
// 由用户手填，不能假设一定有。
type ModelInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"` // 显示名（有就带回）
	ContextWindow int      `json:"context_window,omitempty"`
	MaxOutput     int      `json:"max_tokens,omitempty"`
	Input         []string `json:"input,omitempty"`         // 模态，只留 text / image
	EffortLevels  []string `json:"effort_levels,omitempty"` // 支持的思考强度
	EffortDefault string   `json:"effort_default,omitempty"`
}

// normModalities：上游的模态可能写成 image/png、vision、image_url……
// 模型层只认 text / image（ValidateModel 卡着），所以在这里就收敛掉，
// 不然登记时会把 image/png 原样存进去、被自己的校验拒掉。
func normModalities(in []string) []string {
	out := []string{"text"}
	seen := false
	for _, x := range in {
		switch {
		case strings.HasPrefix(x, "image"), x == "vision", x == "image_url":
			if !seen {
				out = append(out, "image")
				seen = true
			}
		}
	}
	return out
}

// ListModels：拉某个接口的模型清单和能拿到的一切能力。
func ListModels(baseURL, apiKey string) ([]ModelInfo, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("接口地址为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Close = true
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		// 连不上和"连上了但不认你"是两件事，界面上的处置完全不同
		return nil, fmt.Errorf("连不上这个地址：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		// 4xx 的正文常常带原因（"not supported for format oa-compat" 之类），带一段回去；
		// 401/403 和 404 单独说，因为它们的解法最确定
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		switch resp.StatusCode {
		case 401, 403:
			return nil, fmt.Errorf("API Key 不对或没权限（HTTP %d）", resp.StatusCode)
		case 404:
			return nil, fmt.Errorf("这个地址上没有 /models（HTTP 404），检查是不是少了 /v1")
		}
		if msg := strings.TrimSpace(string(snippet)); msg != "" {
			if len(msg) > 120 {
				msg = msg[:120] + "…"
			}
			return nil, fmt.Errorf("上游返回 %d：%s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("上游返回 %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID            string   `json:"id"`
			Name          string   `json:"name"`
			ContextWindow int      `json:"context_window"`
			MaxOutput     int      `json:"max_output_tokens"`
			InputModality []string `json:"input_modalities"`
			// llama.cpp 风格
			Meta struct {
				NCtx int `json:"n_ctx"`
			} `json:"meta"`
			Architecture struct {
				InputModalities []string `json:"input_modalities"`
			} `json:"architecture"`
			Effort struct {
				SupportedLevels []string `json:"supported_levels"`
				DefaultLevel    string   `json:"default_level"`
			} `json:"effort"`
			// OpenCode / OpenRouter 风格
			Limit struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			} `json:"limit"`
			Modalities struct {
				Input []string `json:"input"`
			} `json:"modalities"`
			Capabilities struct {
				Reasoning struct {
					Effort []string `json:"effort"`
				} `json:"reasoning"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("这个地址返回的不是 /models 的 JSON：%v", err)
	}
	list := make([]ModelInfo, 0, len(out.Data))
	for _, d := range out.Data {
		if d.ID == "" {
			continue
		}
		m := ModelInfo{ID: d.ID, Name: d.Name, ContextWindow: d.ContextWindow, MaxOutput: d.MaxOutput}
		if m.ContextWindow == 0 {
			m.ContextWindow = d.Meta.NCtx
		}
		if m.ContextWindow == 0 {
			m.ContextWindow = d.Limit.Context
		}
		if m.MaxOutput == 0 {
			m.MaxOutput = d.Limit.Output
		}
		m.Input = d.InputModality
		if len(m.Input) == 0 {
			m.Input = d.Architecture.InputModalities
		}
		if len(m.Input) == 0 {
			m.Input = d.Modalities.Input
		}
		if len(m.Input) > 0 {
			m.Input = normModalities(m.Input)
		}
		m.EffortLevels = d.Effort.SupportedLevels
		if len(m.EffortLevels) == 0 {
			m.EffortLevels = d.Capabilities.Reasoning.Effort
		}
		m.EffortDefault = d.Effort.DefaultLevel
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}
