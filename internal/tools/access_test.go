package tools

import (
	"os"
	"testing"

	"xaihi/internal/config"
	"xaihi/internal/toolmeta"
)

func accessRegistry() map[string]*Spec {
	return map[string]*Spec{
		"bash":        {Name: "bash", Category: toolmeta.GroupChat},
		"memory_save": {Name: "memory_save", Category: toolmeta.GroupPersona},
		"wiki_write":  {Name: "wiki_write", Category: toolmeta.GroupPersona},
		"hook_add":    {Name: "hook_add", Category: toolmeta.GroupPersist},
		"view_image":  {Name: "view_image", Category: toolmeta.GroupView},
	}
}

func TestForProfileEmptyToolsMeansNoTools(t *testing.T) {
	got := ForProfile(accessRegistry(), config.Profile{})
	if len(got) != 0 {
		t.Errorf("空 Tools 不该暴露任何工具：%+v", got)
	}
}

func TestForProfileLegacyPersistentOnlyEnablesMemory(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	if err := config.SavePersona("xaihi", "人格"); err != nil {
		t.Fatal(err)
	}
	p := config.Profile{
		Persona: "xaihi", Persistent: true, Input: []string{"text"},
		Tools: []string{"bash", "memory_save", "wiki_write", "hook_add", "view_image"},
	}
	got := ForProfile(accessRegistry(), p)
	if got["memory_save"] == nil || got["hook_add"] == nil || got["bash"] == nil {
		t.Errorf("旧 persistent 应保留 memory/hook：%+v", got)
	}
	if got["wiki_write"] != nil {
		t.Errorf("wiki 是新能力，不该由旧 persistent 默认开启：%+v", got)
	}
	if got["view_image"] != nil {
		t.Errorf("纯文本模型不该得到图像工具：%+v", got)
	}
}

func TestForProfilePersonaConfigControlsMemoryAndWiki(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	if err := config.SavePersona("xaihi", "人格"); err != nil {
		t.Fatal(err)
	}
	if err := config.SavePersonaProfile(config.PersonaProfile{Name: "xaihi", Memory: true, Wiki: true}); err != nil {
		t.Fatal(err)
	}
	p := config.Profile{
		Persona: "xaihi", Persistent: false, Input: []string{"text", "image"},
		Tools: []string{"memory_save", "wiki_write", "hook_add", "view_image"},
	}
	got := ForProfile(accessRegistry(), p)
	if got["memory_save"] == nil || got["wiki_write"] == nil {
		t.Errorf("人格 JSON 应独立开启 memory/wiki：%+v", got)
	}
	if got["hook_add"] != nil {
		t.Errorf("会话 persistent=false 仍应禁用旧持久化工具：%+v", got)
	}
	if got["view_image"] == nil {
		t.Errorf("有图像能力时应保留 view_image：%+v", got)
	}

	if err := config.SavePersonaProfile(config.PersonaProfile{Name: "xaihi", Memory: false, Wiki: false}); err != nil {
		t.Fatal(err)
	}
	got = ForProfile(accessRegistry(), p)
	if got["memory_save"] != nil || got["wiki_write"] != nil {
		t.Errorf("人格关闭能力后运行时必须禁用：%+v", got)
	}
}

func TestForProfileBrokenPersonaConfigFailsClosed(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	if err := config.SavePersona("xaihi", "人格"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.PersonaProfilePath("xaihi"), []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := config.Profile{Persona: "xaihi", Persistent: true, Tools: []string{"memory_save", "wiki_write"}}
	if got := ForProfile(accessRegistry(), p); len(got) != 0 {
		t.Errorf("人格配置损坏时应失败关闭：%+v", got)
	}
}

func TestForProfilePersonaToolsRequirePersona(t *testing.T) {
	p := config.Profile{Persistent: true, Tools: []string{"memory_save", "wiki_write"}}
	if got := ForProfile(accessRegistry(), p); len(got) != 0 {
		t.Errorf("没有人格时不该有人格工具：%+v", got)
	}
}
