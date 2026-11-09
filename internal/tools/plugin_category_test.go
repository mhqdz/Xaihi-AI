package tools

import (
	"os"
	"path/filepath"
	"testing"

	"xaihi/internal/toolmeta"
)

// 插件清单里的中文分组要能原样读出来；留空归到「扩展」。
// 这条同时验证 UTF-8 走通：文件 → 结构体 → Spec.Category。
func TestPluginCategory(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("draw", `{"name":"draw","category":"绘图","description":"画图","params":{},"command":["bash","-c","echo {}"]}`)
	write("bare", `{"name":"bare","description":"没分组","params":{},"command":["bash","-c","echo {}"]}`)

	m, warns := LoadPlugins(dir)
	if len(warns) != 0 {
		t.Fatalf("警告: %v", warns)
	}
	if got := m["draw"].Category; got != "绘图" {
		t.Errorf("中文分组没读出来: %q", got)
	}
	if got := m["bare"].Category; got != toolmeta.GroupExt {
		t.Errorf("没声明分组应该是「扩展」，得到 %q", got)
	}
}

// 预置分组名与顺序
func TestToolmetaGroups(t *testing.T) {
	if got := toolmeta.Category("bash"); got != toolmeta.GroupChat {
		t.Errorf("bash 分类 = %q", got)
	}
	if got := toolmeta.Category("view_image"); got != toolmeta.GroupView {
		t.Errorf("view_image 分类 = %q", got)
	}
	if got := toolmeta.Category("memory_save"); got != toolmeta.GroupPersist {
		t.Errorf("memory_save 分类 = %q", got)
	}
	if toolmeta.Order(toolmeta.GroupChat) != 0 || toolmeta.Order(toolmeta.GroupView) != 2 {
		t.Errorf("预置顺序不对")
	}
	if toolmeta.Order("绘图") != -1 {
		t.Errorf("未知分组 Order 应该是 -1")
	}
	// needs 按分组判断
	if !toolmeta.NeedsPersistentTool("hook_add") || toolmeta.NeedsPersistentTool("bash") {
		t.Error("NeedsPersistentTool 判断错")
	}
	if !toolmeta.NeedsVisionTool("screenshot") || toolmeta.NeedsVisionTool("bash") {
		t.Error("NeedsVisionTool 判断错")
	}
}
