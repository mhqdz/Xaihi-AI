package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentd/internal/logx"
)

// Endpoint：一个可用的模型接口（地址 + 凭据 + 协议）。
//
// 和 Profile 拆开，是因为"接哪儿"和"用哪个模型怎么用"是两件事：
// 同一个本地服务会被好几个 profile 共用（不同思考强度、不同 system），
// 以前每个 profile 各存一份地址，改一次地址要改三处。
//
// 关系对齐"工具 → 模型"：接口是全局注册的，模型通过 Endpoint 字段引用它。
type Endpoint struct {
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key,omitempty"` // 空=回落全局 cfg.APIKey
	Provider string `json:"provider,omitempty"`
	Source   string `json:"source,omitempty"` // 来源 key（见 sources.json），只用于表单预填和展示
}

// Source：来源模板。放在 sources.json 里而不是写死在代码里 ——
// 以后多一个服务商只要加一条，不用重新编译。
type Source struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	BaseURL  string `json:"base_url,omitempty"` // 预填模板；空 = 只给个名字，地址手填
	Provider string `json:"provider,omitempty"`
}

var (
	emu    sync.RWMutex
	ecache []Endpoint
	bmu    sync.RWMutex
	bcache []Source
)

func nowStamp() string { return time.Now().Format("20060102-150405") }

func endpointsPath() string { return DataPath("endpoints.json") }
func sourcesPath() string   { return DataPath("sources.json") }

// 内置来源：sources.json 不存在时用这一份，用户改文件就能覆盖
func defaultSources() []Source {
	return []Source{
		{Key: "local", Label: "本地 / llama.cpp", BaseURL: "http://127.0.0.1:18320/180/v1", Provider: ""},
		{Key: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", Provider: "openai"},
		{Key: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1", Provider: "openai"},
		{Key: "custom", Label: "自定义", BaseURL: "", Provider: ""},
	}
}

// LoadSources：读 sources.json；没有就用内置那份（不落盘，避免覆盖用户想删的项）
func LoadSources() []Source {
	bmu.RLock()
	if bcache != nil {
		out := bcache
		bmu.RUnlock()
		return out
	}
	bmu.RUnlock()

	list := defaultSources()
	if b, err := os.ReadFile(sourcesPath()); err == nil {
		var got []Source
		if err := json.Unmarshal(b, &got); err == nil && len(got) > 0 {
			list = got
		} else if err != nil {
			logx.Warn("sources.json 解析失败，用内置列表：%v", err)
		}
	}
	bmu.Lock()
	bcache = list
	bmu.Unlock()
	return list
}

// SaveSources：把来源写回文件（界面里改的话用）
func SaveSources(list []Source) error {
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(sourcesPath(), append(b, '\n'), 0o644); err != nil {
		return err
	}
	bmu.Lock()
	bcache = list
	bmu.Unlock()
	return nil
}

func LoadEndpoints() []Endpoint {
	emu.RLock()
	if ecache != nil {
		out := ecache
		emu.RUnlock()
		return out
	}
	emu.RUnlock()

	var list []Endpoint
	if b, err := os.ReadFile(endpointsPath()); err == nil {
		if err := json.Unmarshal(b, &list); err != nil {
			logx.Warn("endpoints.json 解析失败：%v", err)
			list = nil
		}
	}
	emu.Lock()
	ecache = list
	emu.Unlock()
	return list
}

func writeEndpoints(list []Endpoint) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(endpointsPath(), append(b, '\n'), 0o644); err != nil {
		return err
	}
	emu.Lock()
	ecache = list
	emu.Unlock()
	return nil
}

func GetEndpoint(name string) (Endpoint, bool) {
	for _, e := range LoadEndpoints() {
		if e.Name == name {
			return e, true
		}
	}
	return Endpoint{}, false
}

func ValidateEndpoint(e Endpoint) error {
	if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.BaseURL) == "" {
		return fmt.Errorf("名称和接口地址都不能为空")
	}
	return nil
}

func UpsertEndpoint(e Endpoint) error {
	if err := ValidateEndpoint(e); err != nil {
		return err
	}
	list := append([]Endpoint{}, LoadEndpoints()...)
	found := false
	for i := range list {
		if list[i].Name == e.Name {
			list[i] = e
			found = true
		}
	}
	if !found {
		list = append(list, e)
	}
	return writeEndpoints(list)
}

// DeleteEndpoint：删接口前要确认没有模型挂在这个接口下 ——
// 模型没了接口就没有地址，成了一堆配了窗口和模态却发不出去的条目。
// 两层的引用都要查：models.json（模型层）和 profiles.json（配置层）。
func DeleteEndpoint(name string) error {
	for _, m := range LoadModels() {
		if m.Endpoint == name {
			return fmt.Errorf("还有模型挂在这个接口下（%s），先指到别的接口或删掉它", m.Name)
		}
	}
	for _, p := range LoadProfiles() {
		if p.Endpoint == name {
			return fmt.Errorf("还有配置在用这个接口（%s），先改掉它们", p.Name)
		}
	}
	list := LoadEndpoints()
	out := list[:0:0]
	removed := false
	for _, e := range list {
		if e.Name == name {
			removed = true
			continue
		}
		out = append(out, e)
	}
	if !removed {
		return fmt.Errorf("没有这个接口")
	}
	return writeEndpoints(out)
}

// SplitURL：从地址里猜一个可读的接口名（迁移时用）
func guessEndpointName(baseURL string) string {
	u := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	u = strings.ReplaceAll(u, ":", "-")
	u = strings.ReplaceAll(u, "/", "-")
	u = strings.ReplaceAll(u, ".", "-")
	if u == "" {
		u = "endpoint"
	}
	if len(u) > 41 {
		u = u[:41]
	}
	return u
}

// MigrateEndpoints：把老 profile 里各自存的 base_url 抽成共享接口。
//
// 按 base_url + api_key 去重：指向同一个上游的几个 profile 合成一个接口。
// 只在"profile 有 base_url 但没有 endpoint"时动手，所以可以重复执行。
// 动手前先备份 profiles.json —— 这是在改用户数据。
func MigrateEndpoints() (int, int) {
	profs := LoadProfiles()
	need := false
	for _, p := range profs {
		if p.BaseURL != "" && p.Endpoint == "" {
			need = true
			break
		}
	}
	if !need {
		return 0, 0
	}

	// 备份
	if b, err := os.ReadFile(profilesPath()); err == nil {
		bak := profilesPath() + ".bak-migrate-" + nowStamp()
		if err := os.WriteFile(bak, b, 0o644); err == nil {
			logx.Info("profiles 已备份到 %s", filepath.Base(bak))
		}
	}

	endpoints := append([]Endpoint{}, LoadEndpoints()...)
	byKey := map[string]string{} // baseURL|apiKey -> endpoint name
	for _, e := range endpoints {
		byKey[e.BaseURL+"|"+e.APIKey] = e.Name
	}
	taken := map[string]bool{}
	for _, e := range endpoints {
		taken[e.Name] = true
	}

	created := 0
	for i := range profs {
		p := &profs[i]
		if p.BaseURL == "" || p.Endpoint != "" {
			continue
		}
		key := p.BaseURL + "|" + p.APIKey
		name, ok := byKey[key]
		if !ok {
			name = guessEndpointName(p.BaseURL)
			// 重名就加后缀
			base := name
			for n := 2; taken[name]; n++ {
				name = fmt.Sprintf("%s-%d", base, n)
			}
			taken[name] = true
			ep := Endpoint{Name: name, BaseURL: p.BaseURL, APIKey: p.APIKey, Provider: p.Provider, Source: "custom"}
			endpoints = append(endpoints, ep)
			byKey[key] = name
			created++
		}
		p.Endpoint = name
		// 地址和 key 现在归接口管；profile 里留空，避免出现两份真源
		p.BaseURL = ""
		p.APIKey = ""
	}
	if created > 0 {
		if err := writeEndpoints(endpoints); err != nil {
			logx.Warn("写 endpoints.json 失败：%v", err)
			return 0, 0
		}
	}
	if err := writeProfiles(profs); err != nil {
		logx.Warn("写回 profiles 失败：%v", err)
		return created, 0
	}
	logx.Info("endpoint 迁移完成：新建 %d 个接口，改了 %d 个 profile", created, len(profs))
	return created, len(profs)
}
