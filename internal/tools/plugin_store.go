package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"xaihi/internal/config"
)

// PluginDir：外置插件清单目录
func PluginDir() string {
	return config.DataPath("tools.d")
}

// PluginDraft：前端提交的插件定义（与清单文件字段对齐）
type PluginDraft struct {
	Name string `json:"name"`
	// OriginalName：这次保存前它叫什么（旧文件名去掉 .json）。
	// 非空且与 Name 不同就是改名：写到 <name>.json，再把旧文件删掉。
	OriginalName string `json:"originalName,omitempty"`
	// OldName：改名前它在工具列表里叫什么。和 OriginalName 不是一回事 ——
	// 改过一次名之后，文件名（OriginalName）和显示名（OldName）就已经分叉了：
	// 文件还叫最初的 rn_a.json，列表里显示的可能是 rn_b，这次改成 rn_c。
	OldName string `json:"oldName,omitempty"`

	Description string   `json:"description"`
	Command     []string `json:"command"`
	TimeoutMs   int      `json:"timeoutMs"`
	Persistent  bool     `json:"persistent"`
	// Category：分组名（中文，例如「绘图」）。留空归到「扩展」。
	// 分组不需要预先注册 —— 界面按"出现过的分组"动态渲染。
	Category string            `json:"category,omitempty"`
	Params   map[string]any    `json:"params"`
	Required []string          `json:"required"`
	Env      map[string]string `json:"env"`
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
	target := filepath.Join(dir, name+".json")
	orig := strings.TrimSpace(d.OriginalName)
	oldPath := ""
	if orig != "" && orig != name {
		oldPath = filepath.Join(dir, orig+".json")
		// 新名字的文件已经存在、又不是自己 → 那个名字被别的插件占着
		if oldPath != target {
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("%s.json 已存在，换一个名字", name)
			}
		}
	}
	// 先写新文件，成功后再删旧的：写失败时旧文件还在，不会两个都丢。
	// 文件名必须跟着 name 走 —— 它就是这个插件的身份。
	// 曾经试过"只改内容、文件名不动"，结果新建一个与旧文件名同名的插件时
	// 会把改名后的那个直接覆盖掉（工具凭空消失）。
	if err := config.WriteFileAtomic(target, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if oldPath != "" && oldPath != target {
		if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("写入成功，但旧文件 %s 删不掉：%w", filepath.Base(oldPath), err)
		}
	}
	return nil
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
