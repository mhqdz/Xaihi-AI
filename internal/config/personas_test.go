package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersonaSaveLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XAIHI_DATA_DIR", dir)
	if err := SavePersona("xaihi", "你是 Xaihi。说话简短。"); err != nil {
		t.Fatalf("SavePersona: %v", err)
	}
	got, err := LoadPersona("xaihi")
	if err != nil || got != "你是 Xaihi。说话简短。" {
		t.Fatalf("LoadPersona = %q, %v", got, err)
	}
	if names := ListPersonas(); len(names) != 1 || names[0] != "xaihi" {
		t.Fatalf("ListPersonas = %v", names)
	}
	// 空名字 = 不用人格，不算错
	if p, err := LoadPersona(""); p != "" || err != nil {
		t.Fatalf("空名字应返回空串: %q %v", p, err)
	}
	// 非法名字挡在写入前
	if err := SavePersona("../evil", "x"); err == nil {
		t.Error("路径穿越应该被拒")
	}
	if err := SavePersona("ok", "  "); err == nil {
		t.Error("空内容应该被拒")
	}
}

// 校验：persistent 开关、人格存在性、image 能力
func TestValidatePersonaAndTools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XAIHI_DATA_DIR", dir)
	_ = SavePersona("xaihi", "你是 Xaihi。")
	base := Profile{
		Name: "p", Model: "m", Endpoint: "e", ContextWindow: 1000,
		Input: []string{"text"}, System: "干活",
	}
	// 引用不存在的人格
	bad := base
	bad.Persona = "nope"
	if err := ValidateProfileWith(bad, map[string]bool{"e": true}); err == nil {
		t.Error("不存在的人格应该被拒")
	}
	// 开持久化但没人格
	bad2 := base
	bad2.Persistent = true
	bad2.Tools = []string{"memory_save"}
	if err := ValidateProfileWith(bad2, map[string]bool{"e": true}); err == nil {
		t.Error("持久化会话没人格应该被拒")
	}
	// 有人格 + persistent + memory 工具 → 通过
	ok := base
	ok.Persona = "xaihi"
	ok.Persistent = true
	ok.Tools = []string{"memory_save", "hook_add", "bash"}
	if err := ValidateProfileWith(ok, map[string]bool{"e": true}); err != nil {
		t.Errorf("合法组合被拒: %v", err)
	}
	// 没 persistent 却选了记忆工具
	bad3 := base
	bad3.Persona = "xaihi"
	bad3.Tools = []string{"memory_save"}
	if err := ValidateProfileWith(bad3, map[string]bool{"e": true}); err == nil {
		t.Error("未开持久化却选记忆工具应该被拒")
	}
	// 不能看图却选了截图工具
	bad4 := base
	bad4.Tools = []string{"screenshot"}
	if err := ValidateProfileWith(bad4, map[string]bool{"e": true}); err == nil {
		t.Error("input 没有 image 却选截图应该被拒")
	}
	// 能看图就能选
	okImg := base
	okImg.Input = []string{"text", "image"}
	okImg.Tools = []string{"screenshot", "view_image"}
	if err := ValidateProfileWith(okImg, map[string]bool{"e": true}); err != nil {
		t.Errorf("看图能力齐了却被拒: %v", err)
	}
}

func TestPersonaDirDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XAIHI_DATA_DIR", dir)
	if got := PersonasDir(); got != filepath.Join(dir, "personas") {
		t.Errorf("PersonasDir = %s", got)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
