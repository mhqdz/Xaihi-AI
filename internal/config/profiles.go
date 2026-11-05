package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
)

// Profile = 模型 + 思考强度 + 提示词 + 工具集。前端通过 CRUD 管理，解码/校验都在 Go。
type Profile struct {
	Name            string   `json:"name"`
	Endpoint        string   `json:"endpoint,omitempty"` // 引用 endpoints.json 里的接口名
	BaseURL         string   `json:"base_url,omitempty"` // 迁移前的老字段；迁移后留空
	Model           string   `json:"model"`
	APIKey          string   `json:"api_key,omitempty"`          // 空=用全局 cfg.APIKey；云端填自己的 key
	Provider        string   `json:"provider,omitempty"`         // ""=本地/兼容(发 reasoning 扩展) | "openai"=只发标准字段
	ReasoningEffort string   `json:"reasoning_effort,omitempty"` // none|low|medium|high
	System          string   `json:"system"`
	Tools           []string `json:"tools"`
	KeepMessages    int      `json:"keep_messages,omitempty"`
	Summarizer      string   `json:"summarizer,omitempty"`        // 用哪个 profile 写摘要；空=自己
	MaxPromptTokens int      `json:"max_prompt_tokens,omitempty"` // 该模型的输入 token 上限；0 = 默认 105000（模型窗口 131072 的 80%）
	ContextWindow   int      `json:"context_window,omitempty"`    // 该模型的窗口(tokens)；0 = 自动读上游 /models 的 meta.n_ctx
	ReasoningBudget int      `json:"reasoning_budget,omitempty"`  // 单轮思考 token 预算；0 = 默认 4096
	MaxTokensOut    int      `json:"max_tokens_out,omitempty"`    // 单次回复(含思考)的输出上限；0 = 默认 8192
	CompactPct      int      `json:"compact_pct,omitempty"`       // 达到上限的百分之几时自动压缩/摘要；0 = 默认 80
	Builtin         bool     `json:"builtin,omitempty"`
}

var (
	pmu    sync.RWMutex
	pcache []Profile
	plock  *os.File
)

func profilesPath() string {
	if v := os.Getenv("AGENTD_PROFILES"); v != "" {
		return v
	}
	return DataPath("profiles.json")
}

func defaults() []Profile {
	return []Profile{
		{Name: "180-think", BaseURL: "http://127.0.0.1:18320/180/v1", Model: "qwen3.8-flash-next-iq3_xxs",
			ReasoningEffort: "high", Builtin: true, KeepMessages: 16,
			Tools: []string{"bash", "screenshot", "kb_search", "recall"}},
		{Name: "180-low", BaseURL: "http://127.0.0.1:18320/180/v1", Model: "qwen3.8-flash-next-iq3_xxs",
			ReasoningEffort: "low", Builtin: true, KeepMessages: 16,
			Tools: []string{"bash", "screenshot", "kb_search", "recall"}},
		{Name: "9-exec", BaseURL: "http://127.0.0.1:18320/9/v1", Model: "Qwen3.5-9B-int4-ov",
			ReasoningEffort: "none", Builtin: true, KeepMessages: 8,
			Tools: []string{"bash"}},
	}
}

// LoadProfiles：首次没有文件就用内置默认（不写盘）。
func LoadProfiles() []Profile {
	pmu.Lock()
	defer pmu.Unlock()
	if pcache != nil {
		return pcache
	}
	b, err := os.ReadFile(profilesPath())
	if err != nil {
		pcache = defaults()
		return pcache
	}
	var list []Profile
	if json.Unmarshal(b, &list) != nil || len(list) == 0 {
		pcache = defaults()
		return pcache
	}
	pcache = list
	return pcache
}

func GetProfile(name string) (Profile, bool) {
	for _, p := range LoadProfiles() {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// writeProfiles：flock 串行化多进程写，原子替换。
// RenameToolInProfiles：工具改名后，把各 profile 的 tools 列表里的旧名换成新名。
//
// 不做这一步的话，改完名字那个工具会从所有模型的可用列表里消失 ——
// 用户看到的是"改个名字把插件弄没了"。返回被改动的引用条数。
func RenameToolInProfiles(oldName, newName string) (int, error) {
	if oldName == "" || newName == "" || oldName == newName {
		return 0, nil
	}
	list := append([]Profile{}, LoadProfiles()...)
	n := 0
	for i := range list {
		for j, t := range list[i].Tools {
			if t == oldName {
				list[i].Tools[j] = newName
				n++
			}
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, writeProfiles(list)
}

// Resolve：这个 profile 实际要连的地址/凭据/协议。
//
// 地址归接口管（Endpoint），profile 只声明"用哪个接口"。
// 老数据还没迁移时回落到 BaseURL，保证迁移前后都能跑。
func (p Profile) Resolve() (baseURL, apiKey, provider string) {
	if p.Endpoint != "" {
		if e, ok := GetEndpoint(p.Endpoint); ok {
			return e.BaseURL, e.APIKey, e.Provider
		}
	}
	return p.BaseURL, p.APIKey, p.Provider
}

func writeProfiles(list []Profile) error {
	p := profilesPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	pcache = list
	return nil
}

func ValidateProfile(p Profile) error {
	if p.Name == "" || p.Model == "" {
		return fmt.Errorf("名称和模型都不能为空")
	}
	// 地址不再由模型自己带：要么引用一个接口，要么（老数据）还留着 base_url
	if p.Endpoint == "" && p.BaseURL == "" {
		return fmt.Errorf("要选一个接口")
	}
	if p.ReasoningEffort != "" && p.ReasoningEffort != "none" && p.ReasoningEffort != "low" &&
		p.ReasoningEffort != "medium" && p.ReasoningEffort != "high" {
		return fmt.Errorf("reasoning_effort 只能是 none|low|medium|high")
	}
	return nil
}

func UpsertProfile(p Profile) error {
	if err := ValidateProfile(p); err != nil {
		return err
	}
	list := append([]Profile{}, LoadProfiles()...)
	replaced := false
	for i := range list {
		if list[i].Name == p.Name {
			if list[i].Builtin {
				p.Builtin = true
			}
			list[i] = p
			replaced = true
		}
	}
	if !replaced {
		list = append(list, p)
	}
	return writeProfiles(list)
}

func DeleteProfile(name string) error {
	list := append([]Profile{}, LoadProfiles()...)
	out := make([]Profile, 0, len(list))
	found := false
	for _, p := range list {
		if p.Name == name {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return fmt.Errorf("没有这个 profile")
	}
	return writeProfiles(out)
}
