package tools

import (
	"agentd/internal/config"
	"encoding/json"
	"os"
	"path/filepath"
)

// OverridePath 返回工具说明覆盖文件（UI 里改过的说明写在这里）。
func OverridePath() string {
	return config.DataPath("tools.json")
}

// LoadOverrides 读取 {"bash":"说明", "kb_search":"说明"} 或旧格式 {"bash":{"description":"…"}}。
// 结构体里多余的字段会被忽略（Go 的 json 默认行为）。
func LoadOverrides() map[string]string {
	b, err := os.ReadFile(OverridePath())
	if err != nil {
		return map[string]string{}
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	for k, v := range raw {
		var s string
		if json.Unmarshal(v, &s) == nil {
			out[k] = s
			continue
		}
		var obj struct {
			Description string `json:"description"`
		}
		if json.Unmarshal(v, &obj) == nil && obj.Description != "" {
			out[k] = obj.Description
		}
	}
	return out
}

// SaveOverride 写入某个工具的说明。
func SaveOverride(name, desc string) error {
	all := LoadOverrides()
	all[name] = desc
	b, _ := json.MarshalIndent(all, "", "  ")
	if err := os.MkdirAll(filepath.Dir(OverridePath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(OverridePath(), b, 0o644)
}

// applyOverrides 把覆盖后的说明写回注册表。
func applyOverrides(m map[string]*Spec) {
	for name, desc := range LoadOverrides() {
		if sp, ok := m[name]; ok && desc != "" {
			sp.Description = desc
		}
	}
}
