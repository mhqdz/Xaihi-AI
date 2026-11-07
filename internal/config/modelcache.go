package config

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"

	"agentd/internal/logx"
)

// 模型能力缓存：不是配置层，是"上次问上游要到的答案"。
//
// 三层里的模型层（models.json）删掉之后，窗口 / 输出上限 / 模态 / 档位这些
// "模型事实"仍然需要一个落脚点，二选一都不行：
//
//	只用实时结果 → GET /api/profiles 变成要联网，接口一挂页面就等超时，
//	               agent 启动时也算不出上下文预算；
//	存进 profile → 180-low/medium/think 各存一份 131072，上游改了要改三处。
//
// 所以放这里：由 agentd 在拉列表时自动写，**没有界面、不给人手改、删掉只会导致重拉**。
// profile 上仍留 context_window / max_tokens_out / input / effort_levels 作为覆盖
// （0 / 空 = 用缓存），正常路径一个都不用填。
//
// 时效性：At 记着这份数据是什么时候问到的。界面上打开模型下拉会强制重拉
// （成功即覆盖并刷新 At），启动时后台对超过 24h 的条目补一次；界面状态行永远显示
// "刚刚 / N 分钟前 / N 天前"，所以缓存不会悄悄变旧而没人察觉。
type CachedModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	ContextWindow int      `json:"context_window,omitempty"`
	MaxTokens     int      `json:"max_tokens,omitempty"`
	Input         []string `json:"input,omitempty"`         // 只留 text / image
	EffortLevels  []string `json:"effort_levels,omitempty"` // 上游说的档位
}

// CacheEntry 是"某个接口在上游看到的全部模型"。
type CacheEntry struct {
	At     time.Time     `json:"at"`
	Models []CachedModel `json:"models"`
}

var (
	cmu    sync.RWMutex
	ccache map[string]CacheEntry
)

func modelCachePath() string { return DataPath("model-cache.json") }

// LoadModelCache 返回整个缓存（接口名 → 条目）。解析失败按空处理：
// 这是纯派生数据，坏了重拉就行，不能因此让配置读不出来。
func LoadModelCache() map[string]CacheEntry {
	cmu.RLock()
	if ccache != nil {
		out := ccache
		cmu.RUnlock()
		return out
	}
	cmu.RUnlock()

	m := map[string]CacheEntry{}
	if b, err := os.ReadFile(modelCachePath()); err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			logx.Warn("model-cache.json 解析失败（会重新拉）：%v", err)
			m = map[string]CacheEntry{}
		}
	}
	cmu.Lock()
	ccache = m
	cmu.Unlock()
	return m
}

func SaveModelCache(endpoint string, models []CachedModel) error {
	if endpoint == "" {
		return nil
	}
	all := map[string]CacheEntry{}
	for k, v := range LoadModelCache() {
		all[k] = v
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	all[endpoint] = CacheEntry{At: time.Now(), Models: models}

	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(modelCachePath(), append(b, '\n'), 0o644); err != nil {
		return err
	}
	cmu.Lock()
	ccache = all
	cmu.Unlock()
	return nil
}

// ModelCacheEntry 取某个接口的整份缓存。
func ModelCacheEntry(endpoint string) (CacheEntry, bool) {
	e, ok := LoadModelCache()[endpoint]
	return e, ok
}

// CachedModelOf 取某个接口下某个模型的能力，附带这份数据的时间。
func CachedModelOf(endpoint, id string) (CachedModel, time.Time, bool) {
	e, ok := LoadModelCache()[endpoint]
	if !ok {
		return CachedModel{}, time.Time{}, false
	}
	for _, m := range e.Models {
		if m.ID == id {
			return m, e.At, true
		}
	}
	return CachedModel{}, time.Time{}, false
}

// DropModelCache 删掉某个接口的缓存（接口被删时一起清）。
func DropModelCache(endpoint string) error {
	all := LoadModelCache()
	if _, ok := all[endpoint]; !ok {
		return nil
	}
	out := map[string]CacheEntry{}
	for k, v := range all {
		if k == endpoint {
			continue
		}
		out[k] = v
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(modelCachePath(), append(b, '\n'), 0o644); err != nil {
		return err
	}
	cmu.Lock()
	ccache = out
	cmu.Unlock()
	return nil
}

// StaleCacheEndpoints 列出"缓存缺失或超过 maxAge"的接口名。
// 启动时的后台刷新用它决定要问谁，省得每次都把所有接口问一遍。
func StaleCacheEndpoints(maxAge time.Duration) []string {
	all := LoadModelCache()
	seen := map[string]bool{}
	var out []string
	for _, p := range LoadProfiles() {
		if p.Endpoint == "" || seen[p.Endpoint] {
			continue
		}
		seen[p.Endpoint] = true
		if e, ok := all[p.Endpoint]; !ok || time.Since(e.At) > maxAge {
			out = append(out, p.Endpoint)
		}
	}
	sort.Strings(out)
	return out
}
