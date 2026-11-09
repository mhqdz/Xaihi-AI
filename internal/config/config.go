// Package config 只做一件事：决定"跟谁说话"。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	BaseURL      string `json:"base_url"` // 例如 http://127.0.0.1:18320/9/v1（走 path 路由）
	Model        string `json:"model"`
	APIKey       string `json:"api_key"`
	DataDir      string `json:"data_dir"` // JSON 存储目录（P0 先不用 sqlite）
	WebDir       string `json:"web_dir"`  // 前端构建产物目录
	System       string `json:"system"`   // 系统提示（空 = 不加）
	KeepMessages int    `json:"keep_messages"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		BaseURL:      "http://127.0.0.1:18320/9/v1",
		Model:        "Qwen3.5-9B-int4-ov",
		APIKey:       "local-no-auth",
		DataDir:      filepath.Join(home, ".data/xaihi"),
		WebDir:       "web/dist",
		System:       "", // 系统提示只在 profiles.json 里维护（见 docs/DESIGN.md）
		KeepMessages: 16,
	}
}

func path() string {
	if v := os.Getenv("XAIHI_CONFIG"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config/xaihi/config.json")
}

// Load 读配置；文件不存在就返回默认值（不创建文件，避免无意义副作用）。
// DataDir：所有本地数据的根目录。默认 ~/.data/xaihi，XAIHI_DATA_DIR 可覆盖。
//
// 以前只有数据库跟 XAIHI_DATA_DIR 走，profiles.json / settings.json / wake.token /
// shots / tools.d / tasks.d 全都硬编码在 ~/.data/xaihi 下 —— 于是「设了 DATA_DIR 的
// 实例」其实只隔离了一半：库是新的，配置和插件还是的原来那份。
func DataDir() string {
	if v := os.Getenv("XAIHI_DATA_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".data", "xaihi")
}

// DataPath：拼在 DataDir 下的路径，elem 是若干段。
func DataPath(elem ...string) string {
	return filepath.Join(append([]string{DataDir()}, elem...)...)
}

func Load() Config {
	c := Default()
	if b, err := os.ReadFile(path()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if v := os.Getenv("XAIHI_BASE_URL"); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv("XAIHI_MODEL"); v != "" {
		c.Model = v
	}
	if v := os.Getenv("XAIHI_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("XAIHI_MAX_PROMPT_CHARS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
		}
	}
	if v := os.Getenv("XAIHI_WEB_DIR"); v != "" {
		c.WebDir = v
	}
	return c
}
