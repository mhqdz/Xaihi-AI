package llm

import (
	"sync"
	"time"
)

// CapsFor：问一次上游，取某个模型现在的能力（窗口/输出上限/模态/档位）。
//
// 只给"运行时跟随上游"的配置用（profile.auto_caps）：这类接口背后会换模型
// （llm-local 那种代理层），面板里存的是快照，用的时候得看真的。
//
// 60 秒缓存：一轮对话里每条消息都会调到它，不缓存等于每句话打一次 /models。
// 缓存短是故意的 —— auto 的意义就是"上游换了能马上跟上"，一分钟足够。
type capsEntry struct {
	info ModelInfo
	at   time.Time
}

var (
	capsMu    sync.Mutex
	capsCache = map[string]capsEntry{}
	capsTTL   = 60 * time.Second
)

func CapsFor(baseURL, apiKey, id string) (ModelInfo, bool) {
	if baseURL == "" || id == "" {
		return ModelInfo{}, false
	}
	key := baseURL + "|" + id
	capsMu.Lock()
	if e, ok := capsCache[key]; ok && time.Since(e.at) < capsTTL {
		capsMu.Unlock()
		return e.info, true
	}
	capsMu.Unlock()

	list, err := ListModels(baseURL, apiKey)
	if err != nil {
		return ModelInfo{}, false
	}
	var hit ModelInfo
	found := false
	for _, m := range list {
		if m.ID == id {
			hit, found = m, true
			break
		}
	}
	if !found {
		return ModelInfo{}, false
	}
	capsMu.Lock()
	capsCache[key] = capsEntry{info: hit, at: time.Now()}
	capsMu.Unlock()
	return hit, true
}
