package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"agentd/internal/logx"
)

// Model：某个接口下的一个具体模型。
//
// 三层结构的原因：模型的"事实"（窗口多大、支不支持图像）依赖供应商 ——
// 同一串 id 在不同供应商那儿窗口和模态可能不同，所以模型必须挂在接口下面。
//
//	endpoint（接哪儿）→ model（是什么模型）→ profile（拿它怎么用：提示/工具/强度）
//
// 以前是两层（endpoint → profile），profile 直接写裸 model 字符串，于是
// 同一个模型想配两套提示就得写两遍 id、两遍窗口 —— 这正是要拆开的原因。
type Model struct {
	// Name 是唯一标识：<endpoint>/<id>。profile 引用它。
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	ID       string `json:"id"` // 请求里真正发出去的模型名
	// 显示名归上游（llm-local / 供应商）管，agentd 不存 —— 只做转发。
	ContextWindow int      `json:"context_window,omitempty"` // 0 = 读上游 /models 的 meta
	MaxTokens     int      `json:"max_tokens,omitempty"`     // 0 = 默认 8192
	Input         []string `json:"input,omitempty"`          // ["text"] / ["text","image"]；空=按 text
	// EffortLevels：上游说支持的思考强度档位（DeepSeek 这类会给）。
	// 空 = 不知道，界面上就把所有档位都列出来。
	EffortLevels []string `json:"effort_levels,omitempty"`
}

var (
	mmu    sync.RWMutex
	mcache []Model
)

func modelsPath() string { return DataPath("models.json") }

func LoadModels() []Model {
	mmu.RLock()
	if mcache != nil {
		out := mcache
		mmu.RUnlock()
		return out
	}
	mmu.RUnlock()

	var list []Model
	if b, err := os.ReadFile(modelsPath()); err == nil {
		if err := json.Unmarshal(b, &list); err != nil {
			logx.Warn("models.json 解析失败：%v", err)
			list = nil
		}
	}
	mmu.Lock()
	mcache = list
	mmu.Unlock()
	return list
}

func writeModels(list []Model) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(modelsPath(), append(b, '\n'), 0o644); err != nil {
		return err
	}
	mmu.Lock()
	mcache = list
	mmu.Unlock()
	return nil
}

func GetModel(name string) (Model, bool) {
	for _, m := range LoadModels() {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// SupportsImage：这个模型能不能吃图。没声明时按纯文本处理 ——
// 宁可保守（提示"当前模型不支持图像"），也不要发出去被上游拒。
func (m Model) SupportsImage() bool {
	for _, x := range m.Input {
		if x == "image" {
			return true
		}
	}
	return false
}

func ValidateModel(m Model) error { return ValidateModelWith(m, nil) }

// ValidateModelWith：同 ValidateModel，但额外接受一批"本批次里刚声明、还没落盘"
// 的接口名。批量提交时接口和模型常在同一张表单里，只查盘上有什么必然误报
// "没有这个接口"。
func ValidateModelWith(m Model, extraEndpoints map[string]bool) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("模型 id 不能为空")
	}
	if strings.TrimSpace(m.Endpoint) == "" {
		return fmt.Errorf("要指定所属接口")
	}
	if _, ok := GetEndpoint(m.Endpoint); !ok && !extraEndpoints[m.Endpoint] {
		return fmt.Errorf("没有这个接口：%s", m.Endpoint)
	}
	for _, x := range m.Input {
		if x != "text" && x != "image" {
			return fmt.Errorf("input 只能是 text / image（收到 %q）", x)
		}
	}
	for _, x := range m.EffortLevels {
		if !EffortLevelOK(x) {
			return fmt.Errorf("effort_levels 里的 %q 不像档位名（小写英文，可带数字 _ -）", x)
		}
	}
	return nil
}

// EffortLevelOK：档位名**不限定词表**。
//
// 各家叫法不一样：OpenAI 是 low|medium|high，DeepSeek 是 low|high|max，
// OpenCode 还有 minimal|xhigh。agentd 只是把它原样转给上游，没有资格替上游
// 决定它认哪些 —— 白名单式的校验只会在遇到新供应商时把人卡住。
// 挡的是形状：空、带空格、大小写混杂，那些一定是手滑。
func EffortLevelOK(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for i, r := range s {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if i > 0 && ((r >= '0' && r <= '9') || r == '_' || r == '-') {
			continue
		}
		return false
	}
	return true
}

// ModelName 是唯一键的形式：<endpoint>/<id>。
// 用复合名是因为同一个 id 可能出现在不同供应商下，靠 id 单独区分不开。
func ModelName(endpoint, id string) string { return endpoint + "/" + id }

func UpsertModel(m Model) error {
	m.Name = ModelName(m.Endpoint, strings.TrimSpace(m.ID))
	if err := ValidateModel(m); err != nil {
		return err
	}
	list := append([]Model{}, LoadModels()...)
	found := false
	for i := range list {
		if list[i].Name == m.Name {
			list[i] = m
			found = true
		}
	}
	if !found {
		list = append(list, m)
	}
	return writeModels(list)
}

// DeleteModel：被 profile 引用的模型不能删，否则那个配置会突然没有模型可发
func DeleteModel(name string) error {
	for _, p := range LoadProfiles() {
		if p.Model == name {
			return fmt.Errorf("还有配置在用这个模型（%s），先改掉它们", p.Name)
		}
	}
	list := LoadModels()
	out := list[:0:0]
	removed := false
	for _, m := range list {
		if m.Name == name {
			removed = true
			continue
		}
		out = append(out, m)
	}
	if !removed {
		return fmt.Errorf("没有这个模型")
	}
	return writeModels(out)
}

// MigrateModels：把 profile 里裸写的 model 字符串抽成第三层。
//
// 老的 profile.model 是"请求里发出去的模型名"，而 endpoint 已经知道了，
// 所以 (profile.endpoint, profile.model) 就唯一确定一个 Model 条目。
// 同 endpoint 同 id 的几个 profile 会指向同一个 Model —— 这正是要消除的重复配置。
// 窗口/输出上限从第一个遇到的 profile 带过去（它们是模型事实，不该在 profile 里各存一份）。
func MigrateModels() (int, int) {
	profs := LoadProfiles()
	need := false
	for _, p := range profs {
		if p.Model != "" && !p.ModelsMigrated() && p.Endpoint != "" {
			need = true
			break
		}
	}
	if !need {
		return 0, 0
	}

	if b, err := os.ReadFile(profilesPath()); err == nil {
		if err := os.WriteFile(profilesPath()+".bak-models-"+nowStamp(), b, 0o644); err == nil {
			logx.Info("profiles 已备份（模型层迁移）")
		}
	}

	models := append([]Model{}, LoadModels()...)
	byName := map[string]bool{}
	for _, m := range models {
		byName[m.Name] = true
	}

	created := 0
	for i := range profs {
		p := &profs[i]
		if p.Endpoint == "" || p.Model == "" || p.ModelsMigrated() {
			continue
		}
		name := ModelName(p.Endpoint, p.Model)
		if !byName[name] {
			m := Model{Name: name, Endpoint: p.Endpoint, ID: p.Model,
				ContextWindow: p.ContextWindow, MaxTokens: p.MaxTokensOut, Input: []string{"text"}}
			models = append(models, m)
			byName[name] = true
			created++
		}
		p.Model = name
		// 窗口/输出上限是模型事实，搬进 model 层；profile 里清空，避免两份真源
		p.ContextWindow = 0
		p.MaxTokensOut = 0
	}
	if created > 0 {
		if err := writeModels(models); err != nil {
			logx.Warn("写 models.json 失败：%v", err)
			return 0, 0
		}
	}
	if err := writeProfiles(profs); err != nil {
		logx.Warn("写回 profiles 失败：%v", err)
		return created, 0
	}
	logx.Info("模型层迁移完成：新建 %d 个模型，改了 %d 个配置", created, len(profs))
	return created, len(profs)
}
