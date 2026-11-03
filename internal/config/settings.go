package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings 是全局常规配置（与 profile / 工具说明同级）。
type Settings struct {
	ToolRoundsEnabled bool `json:"tool_rounds_enabled"` // 是否限制工具轮次
	ToolRounds        int  `json:"tool_rounds"`         // 限制值（默认 8）

	AutoArchiveEnabled bool `json:"auto_archive_enabled"` // 是否自动归档长期不活跃会话
	AutoArchiveDays    int  `json:"auto_archive_days"`    // 超过多少天未活跃则归档（默认 7）

	ShotsAutoClean bool `json:"shots_auto_clean"` // 是否自动清理截图
	ShotsKeepDays  int  `json:"shots_keep_days"`  // 截图保留天数（默认 7）
}

func settingsPath() string {
	return DataPath("settings.json")
}

func LoadSettings() Settings {
	s := Settings{ToolRoundsEnabled: true, ToolRounds: 8, AutoArchiveEnabled: true, AutoArchiveDays: 7, ShotsAutoClean: true, ShotsKeepDays: 7}
	b, err := os.ReadFile(settingsPath())
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.ToolRounds <= 0 {
		s.ToolRounds = 8
	}
	if s.AutoArchiveDays <= 0 {
		s.AutoArchiveDays = 7
	}
	if s.ShotsKeepDays <= 0 {
		s.ShotsKeepDays = 7
	}
	return s
}

// WriteFileAtomic：先写同目录的 .tmp 再 rename。
// 直接 os.WriteFile 时，写一半被 kill / 磁盘满会留下半截 JSON —— 下次读解析失败，
// 那个文件承载的东西（插件、覆盖说明）就"凭空消失"了。rename 是同目录内的原子操作。
func WriteFileAtomic(path string, b []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func SaveSettings(s Settings) error {
	if s.ToolRounds <= 0 {
		s.ToolRounds = 8
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o755); err != nil {
		return err
	}
	tmp := settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath())
}
