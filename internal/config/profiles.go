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
	Name     string `json:"name"`
	Endpoint string `json:"endpoint,omitempty"` // 引用 endpoints.json 里的接口名
	BaseURL  string `json:"base_url,omitempty"` // 迁移前的老字段；迁移后留空
	// Model 是发给上游的模型名（裸 id，不带接口前缀）。
	// 窗口/模态/档位这些"模型事实"就存在这份配置里 —— 读的时候只看它，
	// 不去问上游、也不查缓存：配置文件必须能离线解析出完整参数。
	// 新建时由"选模型"自动填（下拉拉到的能力），上游没报的就让人自己填。
	Model           string   `json:"model"`
	APIKey          string   `json:"api_key,omitempty"`          // 空=用全局 cfg.APIKey；云端填自己的 key
	Provider        string   `json:"provider,omitempty"`         // ""=本地/兼容(发 reasoning 扩展) | "openai"=只发标准字段
	ReasoningEffort string   `json:"reasoning_effort,omitempty"` // none|low|medium|high
	System          string   `json:"system"`
	Tools           []string `json:"tools"`
	KeepMessages    int      `json:"keep_messages,omitempty"`
	Summarizer      string   `json:"summarizer,omitempty"`        // 用哪个 profile 写摘要；空=自己
	MaxPromptTokens int      `json:"max_prompt_tokens,omitempty"` // 该模型的输入 token 上限；0 = 默认 105000（模型窗口 131072 的 80%）
	ContextWindow   int      `json:"context_window,omitempty"`    // 该模型的窗口(tokens)；必填（保存时校验 >0）
	ReasoningBudget int      `json:"reasoning_budget,omitempty"`  // 单轮思考 token 预算；0 = 默认 4096
	MaxTokensOut    int      `json:"max_tokens_out,omitempty"`    // 单次回复(含思考)的输出上限；0 = 默认 8192
	CompactPct      int      `json:"compact_pct,omitempty"`       // 达到上限的百分之几时自动压缩/摘要；0 = 默认 80
	Input           []string `json:"input,omitempty"`             // ["text"] / ["text","image"]；必填（至少 text）
	EffortLevels    []string `json:"effort_levels,omitempty"`     // 支持的档位标签；空 = 不限（界面列全部）
	// AutoCaps：运行时跟随上游 —— 这份配置每次真正拿来跑（发一轮请求）之前，
	// 先去上游问一次现在的能力，问到了就用上游的，问不到用下面存的值。
	// 应对的是"背后会换模型的接口"（llm-local 那种代理层）：
	// 面板上存的是快照（必填，用于离线/兜底），auto 让它在用的时候总是新的。
	AutoCaps bool `json:"auto_caps,omitempty"`
	Builtin  bool `json:"builtin,omitempty"`
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
			ContextWindow: 131072, Input: []string{"text"},
			ReasoningEffort: "high", Builtin: true, KeepMessages: 16,
			Tools: []string{"bash", "screenshot", "kb_search", "recall"}},
		{Name: "180-low", BaseURL: "http://127.0.0.1:18320/180/v1", Model: "qwen3.8-flash-next-iq3_xxs",
			ContextWindow: 131072, Input: []string{"text"},
			ReasoningEffort: "low", Builtin: true, KeepMessages: 16,
			Tools: []string{"bash", "screenshot", "kb_search", "recall"}},
		{Name: "9-exec", BaseURL: "http://127.0.0.1:18320/9/v1", Model: "Qwen3.5-9B-int4-ov",
			ContextWindow: 32768, Input: []string{"text"},
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

// Resolved：这个 profile 实际要用的东西，把三层拼起来。
type Resolved struct {
	BaseURL       string
	APIKey        string
	Provider      string
	ModelID       string // 发给上游的模型名
	ContextWindow int
	MaxTokens     int
	Input         []string // 模态；空按纯文本
	EffortLevels  []string // 支持的思考强度；空 = 全部
}

// Resolve：把 profile 和它引用的接口拼成实际请求参数。
//
// 能力（窗口/输出/模态/档位）取自 profile 自己 —— 这是有意的：
// 配置文件必须能离线解析，接口挂了也不影响"我这套配置要多少上下文"。
// 上游的数字通过两条路进来：新建时下拉自动填；以及开着 auto_caps 时，
// 每次拉到列表就回写（见 ApplyCapsToAutoProfiles）。
func (p Profile) Resolve() Resolved {
	r := Resolved{
		ModelID:       p.Model,
		ContextWindow: p.ContextWindow,
		MaxTokens:     p.MaxTokensOut,
		Input:         p.Input,
		EffortLevels:  p.EffortLevels,
	}
	if p.Endpoint != "" {
		if e, ok := GetEndpoint(p.Endpoint); ok {
			r.BaseURL, r.APIKey, r.Provider = e.BaseURL, e.APIKey, e.Provider
			return r
		}
	}
	// 迁移前的老数据：接口不存在了也还能跑
	r.BaseURL, r.APIKey, r.Provider = p.BaseURL, p.APIKey, p.Provider
	return r
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

func ValidateProfile(p Profile) error { return ValidateProfileWith(p, nil) }

// ValidateProfileWith：同 ValidateProfile，但额外接受一批"本批次里刚声明、
// 还没落盘"的接口名（批量提交时新建接口 + 新配置常常一起提交）。
func ValidateProfileWith(p Profile, extraEndpoints map[string]bool) error {
	if p.Name == "" || p.Model == "" {
		return fmt.Errorf("名称和模型都不能为空")
	}
	// 地址从哪来：接口名（两层结构下正常路径），或迁移前自己带的 base_url。
	if p.Endpoint == "" && p.BaseURL == "" {
		return fmt.Errorf("要选一个接口（或填接口地址）")
	}
	// 接口名必须真的存在（批量提交时可能在同一次请求里刚建出来）
	if p.Endpoint != "" {
		if _, ok := GetEndpoint(p.Endpoint); !ok && !extraEndpoints[p.Endpoint] {
			return fmt.Errorf("没有这个接口：%s", p.Endpoint)
		}
	}
	// 能力是这份配置的一部分，不是"能省就省"的注释：
	//   窗口留空 → 压缩预算算不出来，猜错就真的丢上下文；
	//   模态留空 → 不知道能不能贴图，界面只能瞎猜。
	// 上游没报的时候，"以后再自动拉一次"也还是同一个结果，所以留空就是无效配置，
	// 必须在保存时说清楚，而不是运行时退回一个默认值装作没事。
	if p.ContextWindow <= 0 {
		return fmt.Errorf("窗口大小必填（上游没报就自己填，比如 131072）")
	}
	if len(p.Input) == 0 {
		return fmt.Errorf("输入模态必填（至少要有 text）")
	}
	for _, x := range p.Input {
		if x != "text" && x != "image" {
			return fmt.Errorf("input 只能是 text / image（收到 %q）", x)
		}
	}
	if p.MaxTokensOut < 0 {
		return fmt.Errorf("输出上限不能是负数")
	}
	for _, x := range p.EffortLevels {
		if !EffortLevelOK(x) {
			return fmt.Errorf("effort_levels 里的 %q 不像档位名（小写英文，可带数字 _ -）", x)
		}
	}
	// 档位只查形状不查词表：各家叫法不同（minimal / xhigh / max），
	// 我们原样转给上游，认不认是上游的事
	if p.ReasoningEffort != "" && !EffortLevelOK(p.ReasoningEffort) {
		return fmt.Errorf("reasoning_effort 只能是英文小写档位名（如 low / high / max）")
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
