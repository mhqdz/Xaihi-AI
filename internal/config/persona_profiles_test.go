package config

import (
	"os"
	"strings"
	"testing"
)

func TestPersonaProfileLegacyFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XAIHI_DATA_DIR", dir)
	if err := SavePersona("xaihi", "你是赛希。"); err != nil {
		t.Fatal(err)
	}
	p, err := ResolvePersonaProfile("xaihi", true)
	if err != nil || p.Exists || !p.Memory || p.Wiki {
		t.Fatalf("旧 persistent 应只开启 memory：%+v, %v", p, err)
	}
	p, err = ResolvePersonaProfile("xaihi", false)
	if err != nil || p.Memory || p.Wiki {
		t.Fatalf("未开 persistent 的旧配置不应开启能力：%+v, %v", p, err)
	}
}

func TestValidateProfileUsesPersonaCapabilities(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	if err := SavePersona("xaihi", "人格"); err != nil {
		t.Fatal(err)
	}
	p := Profile{
		Name: "p", Model: "m", Endpoint: "e", ContextWindow: 1000,
		Input: []string{"text"}, Persona: "xaihi", Tools: []string{"memory_save", "wiki_write"},
	}
	if err := ValidateProfileWith(p, map[string]bool{"e": true}); err == nil {
		t.Fatal("人格未开启 memory/wiki 时不该通过校验")
	}
	if err := SavePersonaProfile(PersonaProfile{Name: "xaihi", Memory: true, Wiki: true}); err != nil {
		t.Fatal(err)
	}
	p.Persistent = false
	if err := ValidateProfileWith(p, map[string]bool{"e": true}); err != nil {
		t.Errorf("人格 JSON 应独立授权 memory/wiki：%v", err)
	}
	p.Persona = ""
	if err := ValidateProfileWith(p, map[string]bool{"e": true}); err == nil {
		t.Error("没有人格时不该允许人格工具")
	}
}

func TestPersonaDreamerValidation(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	if err := SavePersona("xaihi", "人格"); err != nil {
		t.Fatal(err)
	}
	if err := SavePersonaProfile(PersonaProfile{Name: "xaihi", Dreamer: "missing"}); err == nil {
		t.Fatal("不存在的 dreamer profile 应该被拒绝")
	}
	sp := Profile{
		Name: "dreamer", Model: "m", BaseURL: "http://127.0.0.1",
		ContextWindow: 8192, Input: []string{"text"},
	}
	if err := UpsertProfile(sp); err != nil {
		t.Fatal(err)
	}
	if err := SavePersonaProfile(PersonaProfile{Name: "xaihi", Wiki: true, Dreamer: "dreamer"}); err != nil {
		t.Fatal(err)
	}
	pp, _ := LoadPersonaProfile("xaihi")
	got, err := pp.DreamerProfile()
	if err != nil || got.Name != "dreamer" {
		t.Errorf("dreamer 解析不对：%+v, %v", got, err)
	}
	if err := DeleteProfile("dreamer"); err != nil {
		t.Fatal(err)
	}
	if _, err := pp.DreamerProfile(); err == nil {
		t.Error("profile 删除后运行时必须再次发现 dreamer 失效")
	}
}

func TestPersonaProfileSaveLoadAndBackup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XAIHI_DATA_DIR", dir)
	if err := SavePersona("xaihi", "你是赛希。"); err != nil {
		t.Fatal(err)
	}
	if err := SavePersonaProfile(PersonaProfile{Name: "xaihi", Memory: true, Wiki: true}); err != nil {
		t.Fatal(err)
	}
	p, err := ResolvePersonaProfile("xaihi", false)
	if err != nil || !p.Exists || !p.Memory || !p.Wiki {
		t.Fatalf("人格 JSON 应覆盖旧 persistent：%+v, %v", p, err)
	}
	if err := SavePersonaProfile(PersonaProfile{Name: "xaihi", Memory: false, Wiki: true}); err != nil {
		t.Fatal(err)
	}
	p, _ = LoadPersonaProfile("xaihi")
	if p.Memory || !p.Wiki {
		t.Errorf("更新后的配置不对：%+v", p)
	}
	backup, err := os.ReadFile(PersonaProfilePath("xaihi") + ".bak")
	if err != nil || !strings.Contains(string(backup), `"memory": true`) || !strings.Contains(string(backup), `"wiki": true`) {
		t.Errorf("修改前没有留下正确备份：%s, %v", backup, err)
	}
	if err := SavePersonaProfile(PersonaProfile{Name: "missing", Memory: true}); err == nil {
		t.Error("没有人格正文时不该只写人格配置")
	}
	if _, err := LoadPersonaProfile("../evil"); err == nil {
		t.Error("人格配置路径穿越应该被拒")
	}
}
