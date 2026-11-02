package tools

import (
	"agentd/internal/config"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PluginDir：外置插件清单目录
func PluginDir() string {
	return config.DataPath("tools.d")
}

// PluginDraft：前端提交的插件定义（与清单文件字段对齐）
type PluginDraft struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Command     []string          `json:"command"`
	TimeoutMs   int               `json:"timeoutMs"`
	Persistent  bool              `json:"persistent"`
	Params      map[string]any    `json:"params"`
	Required    []string          `json:"required"`
	Env         map[string]string `json:"env"`
}

var pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)

// SavePlugin：新建或覆盖一份清单（内置工具同名禁止）
func SavePlugin(d PluginDraft) error {
	name := strings.TrimSpace(d.Name)
	if !pluginNameRe.MatchString(name) {
		return fmt.Errorf("名字要小写字母开头，只用 a-z 0-9 _（2~41 位）")
	}
	if len(d.Command) == 0 {
		return fmt.Errorf("command 不能为空")
	}
	for _, s := range d.Command {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("command 里有空项")
		}
	}
	if _, exists := Registry()[name]; exists {
		// 已存在的插件允许覆盖；内置工具不允许
		if sp, ok := Registry()[name]; ok && sp.Source != "plugin" {
			return fmt.Errorf("%s 是内置工具，不能覆盖", name)
		}
	}
	m := manifest{
		Name: name, Description: d.Description, Params: d.Params, Required: d.Required,
		Command: d.Command, TimeoutMs: d.TimeoutMs, Persistent: d.Persistent, Env: d.Env,
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	dir := PluginDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+".json"), append(b, '\n'), 0o644)
}

// DeletePlugin：删除一份清单（内置工具不可删）
func DeletePlugin(name string) error {
	sp, ok := Registry()[name]
	if !ok {
		return fmt.Errorf("没有这个工具")
	}
	if sp.Source != "plugin" {
		return fmt.Errorf("%s 是内置工具，不能删除", name)
	}
	path := sp.Path
	if path == "" {
		path = filepath.Join(PluginDir(), name+".json")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}
