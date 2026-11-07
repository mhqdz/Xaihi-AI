package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateProfile(t *testing.T) {
	bad := []Profile{
		{},
		{Name: "a", BaseURL: "", Model: "m"},
		// 能力必填：窗口 / 模态缺一个都是无效配置（不能靠"以后再拉"补上）
		{Name: "a", BaseURL: "u", Model: "m"},
		{Name: "a", BaseURL: "u", Model: "m", ContextWindow: 8192},
		{Name: "a", BaseURL: "u", Model: "m", ContextWindow: 8192, Input: []string{"audio"}},
		// 档位只查形状：大写不行
		{Name: "a", BaseURL: "u", Model: "m", ContextWindow: 8192, Input: []string{"text"}, ReasoningEffort: "Ultra"},
	}
	for i, p := range bad {
		if ValidateProfile(p) == nil {
			t.Fatalf("第 %d 个应校验失败: %+v", i, p)
		}
	}
	good := Profile{Name: "a", BaseURL: "u", Model: "m", ContextWindow: 8192,
		Input: []string{"text"}, ReasoningEffort: "high"}
	if err := ValidateProfile(good); err != nil {
		t.Fatalf("应通过: %v", err)
	}
	// 上游的新档位（OpenCode 的 xhigh / minimal）不该被挡
	good.ReasoningEffort = "xhigh"
	if err := ValidateProfile(good); err != nil {
		t.Fatalf("xhigh 应通过: %v", err)
	}
}

func TestProfilesCRUD(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTD_PROFILES", filepath.Join(dir, "profiles.json"))
	// 首次没有文件 → 内置三个
	if l := LoadProfiles(); len(l) != 3 {
		t.Fatalf("内置应有 3 个，得到 %d", len(l))
	}
	p := Profile{Name: "x", BaseURL: "http://h", Model: "m", Tools: []string{"bash"},
		ContextWindow: 8192, Input: []string{"text"}}
	if err := UpsertProfile(p); err != nil {
		t.Fatal(err)
	}
	if _, ok := GetProfile("x"); !ok {
		t.Fatal("新增后应能取到")
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles.json")); err != nil {
		t.Fatal("应已写盘")
	}
	if err := UpsertProfile(Profile{Name: "x", BaseURL: "", Model: ""}); err == nil {
		t.Fatal("非法更新应失败")
	}
	if err := DeleteProfile("x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := GetProfile("x"); ok {
		t.Fatal("删除后应取不到")
	}
	// 内置也可删（用户要求：取消内置特殊化）
	if err := DeleteProfile("180-think"); err != nil {
		t.Fatalf("内置 profile 应可删: %v", err)
	}
}

func TestProfileSortingAndReload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTD_PROFILES", filepath.Join(dir, "p.json"))
	_ = UpsertProfile(Profile{Name: "z", BaseURL: "u", Model: "m", ContextWindow: 8192, Input: []string{"text"}})
	_ = UpsertProfile(Profile{Name: "a", BaseURL: "u", Model: "m", ContextWindow: 8192, Input: []string{"text"}})
	list := LoadProfiles()
	for i := 1; i < len(list); i++ {
		if list[i-1].Name > list[i].Name {
			t.Fatalf("未按名字排序: %s > %s", list[i-1].Name, list[i].Name)
		}
	}
	// 重新读文件也应得到同样结果（热加载）
	pcache = nil
	list2 := LoadProfiles()
	if len(list2) != len(list) {
		t.Fatalf("重载后数量不一致: %d vs %d", len(list2), len(list))
	}
}
