package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 重名要报错，而不是静默覆盖；改自己则允许。
func TestSavePluginDuplicate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XAIHI_DATA_DIR", dir)

	base := PluginDraft{Name: "dup", Command: []string{"bash", "-c", "echo 1"}, TimeoutMs: 5000}
	if err := SavePlugin(base); err != nil {
		t.Fatalf("第一次保存应该成功: %v", err)
	}
	// 新建撞名（OriginalName 为空）→ 报错
	if err := SavePlugin(base); err == nil || !strings.Contains(err.Error(), "同名") {
		t.Fatalf("撞名应该报错，得到 %v", err)
	}
	// 改自己（OriginalName == name）→ 允许
	again := base
	again.OriginalName = "dup"
	again.Description = "改过"
	if err := SavePlugin(again); err != nil {
		t.Fatalf("改自己应该允许: %v", err)
	}
	// 改名撞到已存在的另一个插件 → 报错
	b := PluginDraft{Name: "other", Command: []string{"bash", "-c", "echo 2"}, TimeoutMs: 5000}
	if err := SavePlugin(b); err != nil {
		t.Fatal(err)
	}
	rename := b
	rename.OriginalName = "other"
	rename.Name = "dup"
	if err := SavePlugin(rename); err == nil {
		t.Fatal("改名撞到已存在的插件应该报错")
	}
	if _, err := os.Stat(filepath.Join(dir, "tools.d", "other.json")); err != nil {
		t.Errorf("失败的那次不该把原文件弄丢: %v", err)
	}
}
