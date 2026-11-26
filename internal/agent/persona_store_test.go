package agent

import (
	"errors"
	"os"
	"testing"

	"xaihi/internal/config"
	"xaihi/internal/memory"
	"xaihi/internal/store"
	"xaihi/internal/wiki"
)

func testAgentWithPersona(name string, persistent bool) *Agent {
	return New(config.Config{Model: "m", BaseURL: "http://127.0.0.1"}, nil, config.Profile{
		Name: "session-" + name, Model: "m", Persona: name, Persistent: persistent, ContextWindow: 131072,
	})
}

func TestPersonaMemoryAndWikiAreIsolated(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	for _, name := range []string{"work", "life"} {
		if err := config.SavePersona(name, name+" 人格"); err != nil {
			t.Fatal(err)
		}
		if err := config.SavePersonaProfile(config.PersonaProfile{Name: name, Memory: true, Wiki: true}); err != nil {
			t.Fatal(err)
		}
	}
	legacy := memory.New(memory.DefaultRoot(config.DataDir()))
	if _, err := legacy.Save("global", "经历", "", "不应猜测归属的旧记忆"); err != nil {
		t.Fatal(err)
	}

	work := testAgentWithPersona("work", false)
	life := testAgentWithPersona("life", false)
	if work.Memory() == nil || life.Memory() == nil || work.Wiki() == nil || life.Wiki() == nil {
		t.Fatal("人格 JSON 应独立启用 memory/wiki，不需要旧 persistent")
	}
	if work.Memory().Root != memory.OwnerRoot(config.DataDir(), "work") || life.Memory().Root != memory.OwnerRoot(config.DataDir(), "life") {
		t.Errorf("memory 根目录不对：%s / %s", work.Memory().Root, life.Memory().Root)
	}
	if entries, _ := work.Memory().List(); len(entries) != 0 {
		t.Errorf("多个人格时不该自动复制旧共享记忆：%+v", entries)
	}
	if _, err := work.Memory().Save("global", "经历", "", "工作记忆"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := life.Memory().List(); len(entries) != 0 {
		t.Errorf("生活人格读到了工作记忆：%+v", entries)
	}
	if _, err := work.Wiki().Write("private", "工作索引", []string{"0004"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := life.Wiki().Read("private"); !errors.Is(err, wiki.ErrNotFound) {
		t.Errorf("生活人格读到了工作 wiki：%v", err)
	}

	// 另一个会话/模型 profile 引用同一人格时，应看到同一份 memory/wiki。
	same := New(config.Config{Model: "other"}, nil, config.Profile{
		Name: "another-model", Model: "other", Persona: "work", ContextWindow: 131072,
	})
	if same.Memory().Root != work.Memory().Root || same.Wiki().Root != work.Wiki().Root {
		t.Errorf("同一人格不应按会话 profile 拆存储：%s / %s", same.Memory().Root, work.Memory().Root)
	}
	if entries, _ := same.Memory().List(); len(entries) != 1 {
		t.Errorf("同一人格的新会话 profile 应共享记忆：%+v", entries)
	}
}

func TestSinglePersonaLegacyMigration(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	if err := config.SavePersona("xaihi", "人格"); err != nil {
		t.Fatal(err)
	}
	if err := config.SavePersonaProfile(config.PersonaProfile{Name: "xaihi", Memory: true}); err != nil {
		t.Fatal(err)
	}
	legacy := memory.New(memory.DefaultRoot(config.DataDir()))
	if _, err := legacy.Save("global", "经历", "", "旧记忆"); err != nil {
		t.Fatal(err)
	}
	a := testAgentWithPersona("xaihi", false)
	if entries, _ := a.Memory().List(); len(entries) != 1 || entries[0].Body != "旧记忆" {
		t.Errorf("唯一人格应继承旧记忆：%+v", entries)
	}
}

func TestConversationPinsPersona(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	st, err := store.NewSQLite(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(config.Config{Model: "m"}, st, config.Profile{Name: "one", Model: "m", Persona: "xaihi"})
	c, err := a.Conversation("")
	if err != nil || c.Persona != "xaihi" {
		t.Fatalf("新会话未固化 persona：%+v, %v", c, err)
	}
	if err := st.Save(c); err != nil {
		t.Fatal(err)
	}
	other := New(config.Config{Model: "m"}, st, config.Profile{Name: "two", Model: "m", Persona: "other"})
	loaded, err := other.Conversation(c.ID)
	if err != nil || loaded.Persona != "xaihi" {
		t.Errorf("切换生成 profile 不应改写已固化归属：%+v, %v", loaded, err)
	}
}

func TestPersonaCapabilitiesCanBeDisabled(t *testing.T) {
	t.Setenv("XAIHI_DATA_DIR", t.TempDir())
	if err := config.SavePersona("xaihi", "人格"); err != nil {
		t.Fatal(err)
	}
	if err := config.SavePersonaProfile(config.PersonaProfile{Name: "xaihi", Memory: false, Wiki: false}); err != nil {
		t.Fatal(err)
	}
	a := testAgentWithPersona("xaihi", true)
	if a.Memory() != nil || a.Wiki() != nil {
		t.Errorf("人格 JSON 明确关闭时不该回退 persistent：%v / %v", a.Memory(), a.Wiki())
	}
	plain := New(config.Config{Model: "m"}, nil, config.Profile{Name: "exec", Model: "m"})
	if plain.Memory() != nil || plain.Wiki() != nil {
		t.Error("无人格的纯执行 profile 不该带 memory/wiki")
	}
	if err := os.WriteFile(config.PersonaProfilePath("xaihi"), []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := testAgentWithPersona("xaihi", true)
	if broken.Memory() != nil || broken.Wiki() != nil {
		t.Error("人格配置损坏时即使 persistent=true 也应失败关闭")
	}
}
