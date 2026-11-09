package config

import (
	"encoding/json"
	"os"
	"sort"
	"time"

	"xaihi/internal/logx"
)

// 三层的模型层（models.json）撤掉之后的回迁：把模型条目里的事实
// 收进两个地方，然后归档这个文件。
//
//   - profile.model 里的 "<接口>/<id>" 拆成 endpoint + 裸 id（上游真正认的字符串）
//   - 能力（窗口/输出上限/模态/档位）写进 model-cache.json，不抄进 profile
//
// 为什么不抄进 profile：这是"上游当时这么说"的派生数据，抄进每套配置就等于
// 180-low/medium/think 各存一份 131072。写进缓存后，profile 只在用户真的
// 想覆盖时才存值，正常路径一个字段都不用填。
type legacyModel struct {
	Name          string   `json:"name"`
	Endpoint      string   `json:"endpoint"`
	ID            string   `json:"id"`
	ContextWindow int      `json:"context_window"`
	MaxTokens     int      `json:"max_tokens"`
	Input         []string `json:"input"`
	EffortLevels  []string `json:"effort_levels"`
}

func modelsPath() string { return DataPath("models.json") }

// loadLegacyModels 读旧的三层文件；没有/看不懂就返回空。
func loadLegacyModels() []legacyModel {
	b, err := os.ReadFile(modelsPath())
	if err != nil {
		return nil
	}
	var list []legacyModel
	if err := json.Unmarshal(b, &list); err != nil {
		logx.Warn("models.json 解析失败，按空处理：%v", err)
		return nil
	}
	return list
}

// MigrateBackLayers：从三层回两层。
//
// 可重复执行：models.json 一旦归档，下次就不再动作。
// 返回 (改了几条 profile, 写了几个接口的缓存)。
func MigrateBackLayers() (int, int) {
	models := loadLegacyModels()
	if len(models) == 0 {
		return 0, 0
	}
	byName := map[string]legacyModel{}
	for _, m := range models {
		byName[m.Name] = m
	}

	profs := LoadProfiles()
	changed := 0
	for i := range profs {
		p := &profs[i]
		m, ok := byName[p.Model]
		if !ok {
			// 裸 id（老的两层数据）：model 本身不用改
			continue
		}
		p.Model = m.ID
		if p.Endpoint == "" {
			p.Endpoint = m.Endpoint
		}
		// 能力收回 profile：这是最后一次"别人替它存着"的搬运，
		// 之后 profile 自己就是唯一事实来源。
		if p.ContextWindow == 0 {
			p.ContextWindow = m.ContextWindow
		}
		if p.MaxTokensOut == 0 {
			p.MaxTokensOut = m.MaxTokens
		}
		if len(p.Input) == 0 {
			p.Input = m.Input
			if len(p.Input) == 0 {
				p.Input = []string{"text"}
			}
		}
		if len(p.EffortLevels) == 0 {
			p.EffortLevels = m.EffortLevels
		}
		changed++
	}
	if changed > 0 {
		if b, err := os.ReadFile(profilesPath()); err == nil {
			if err := os.WriteFile(profilesPath()+".bak-back-"+nowStamp(), b, 0o644); err == nil {
				logx.Info("profiles 已备份（回两层迁移）")
			}
		}
		if err := writeProfiles(profs); err != nil {
			logx.Warn("回两层迁移写 profiles 失败：%v", err)
			return 0, 0
		}
	}

	// 能力按接口分组写进缓存。at 用迁移时刻：这是一次性的接力，
	// 下次任何一方拉列表都会覆盖它。
	grouped := map[string][]CachedModel{}
	for _, m := range models {
		ep := m.Endpoint
		if ep == "" {
			continue
		}
		input := m.Input
		if len(input) == 0 {
			input = []string{"text"}
		}
		grouped[ep] = append(grouped[ep], CachedModel{
			ID: m.ID, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens,
			Input: input, EffortLevels: m.EffortLevels,
		})
	}
	eps := make([]string, 0, len(grouped))
	for k := range grouped {
		eps = append(eps, k)
	}
	sort.Strings(eps)
	for _, ep := range eps {
		if err := SaveModelCache(ep, grouped[ep]); err != nil {
			logx.Warn("写模型能力缓存失败（%s）：%v", ep, err)
		}
	}

	// 归档而不是删：里面可能还有没人引用的条目，留着可查
	archive := modelsPath() + ".bak-back-" + time.Now().Format("20060102-150405")
	if err := os.Rename(modelsPath(), archive); err != nil {
		logx.Warn("归档 models.json 失败：%v", err)
	} else {
		logx.Info("models.json 已归档为 %s", archive)
	}
	return changed, len(eps)
}
