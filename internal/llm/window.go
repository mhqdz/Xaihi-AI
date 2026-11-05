package llm

import (
	"context"
	"encoding/json"
	"fmt"
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

// ListModels：拉某个接口的可用模型名。
//
// 设置面板用它做"选接口 → 自动匹配可用模型"：接口配置好之后，
// 模型选项里才能看到这个接口下有哪些模型，不必手打模型名。
// 和 fetchWindow 走同一个 /models，只是这里要的是列表本身。
func ListModels(baseURL, apiKey string) ([]string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("接口地址为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Close = true
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("上游返回 %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		if d.ID != "" {
			names = append(names, d.ID)
		}
	}
	sort.Strings(names)
	return names, nil
}
