package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir())
	s.Scope = "agentd"
	return s
}

func TestSaveAndList(t *testing.T) {
	s := newStore(t)
	if _, err := s.Save("", "偏好", "conv-1", "不要在我写代码时插话"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := s.Save("", "经历", "", "做过一个 TTS 项目"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List = %d 条，想要 2", len(got))
	}
	for _, e := range got {
		if e.Status() != StatusActive {
			t.Errorf("状态 = %q", e.Status())
		}
		if e.Field(FScope) != "agentd" {
			t.Errorf("scope = %q", e.Field(FScope))
		}
		if e.Created() == "" {
			t.Error("没有 created")
		}
	}
}

func TestGlobalAndProject(t *testing.T) {
	s := newStore(t)
	if _, err := s.Save("global", "偏好", "", "全局的"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save("agentd", "事实", "", "项目的"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List()
	if len(got) != 2 {
		t.Fatalf("两层应该都读到，得到 %d", len(got))
	}
	// 没设 Scope 时只读全局
	s.Scope = ""
	got, _ = s.List()
	if len(got) != 1 || got[0].Body != "全局的" {
		t.Errorf("空 scope 只该读全局，得到 %+v", got)
	}
}

func TestRejectEmptyAndTooLong(t *testing.T) {
	s := newStore(t)
	if _, err := s.Save("", "偏好", "", "   "); err == nil {
		t.Error("空正文应该被拒")
	}
	long := strings.Repeat("字", MaxBodyRunes+1)
	if _, err := s.Save("", "偏好", "", long); err == nil {
		t.Error("超长应该被拒")
	}
}

func TestForgetKeepsFileAndDropsFromList(t *testing.T) {
	s := newStore(t)
	e, _ := s.Save("", "偏好", "", "要被忘掉的")
	if err := s.Forget(e.Name); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if got, _ := s.List(); len(got) != 0 {
		t.Errorf("archived 不该进 List，得到 %d", len(got))
	}
	// 文件还在（可恢复）
	if _, err := os.Stat(filepath.Join(s.Root, "project", "agentd", e.Name+".md")); err != nil {
		t.Errorf("文件应该保留：%v", err)
	}
	if err := s.Restore(e.Name); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got, _ := s.List(); len(got) != 1 {
		t.Errorf("恢复后应该回来，得到 %d", len(got))
	}
	if err := s.Purge(e.Name); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "project", "agentd", e.Name+".md")); !os.IsNotExist(err) {
		t.Error("Purge 后文件应该没了")
	}
}

func TestRenderEmptyAndOrder(t *testing.T) {
	s := newStore(t)
	if got := s.Render(6000); got != "" {
		t.Errorf("空记忆应该渲染成空串，得到 %q", got)
	}
	a, _ := s.Save("", "偏好", "", "第一条")
	b, _ := s.Save("", "偏好", "", "第二条")
	// 手工把 created 改成有先后，验证拼装是升序
	s.setCreated(a.Name, "2026-01-01")
	s.setCreated(b.Name, "2026-02-01")
	out := s.Render(6000)
	i, j := strings.Index(out, "第一条"), strings.Index(out, "第二条")
	if i < 0 || j < 0 || i > j {
		t.Errorf("应按 created 升序拼装：\n%s", out)
	}
	if !strings.Contains(out, "[记忆]") {
		t.Errorf("缺少段落头：%s", out)
	}
}

func TestRenderBudget(t *testing.T) {
	s := newStore(t)
	s.Save("", "偏好", "", strings.Repeat("长", 100))
	big, _ := s.Save("", "偏好", "", strings.Repeat("新", 3000))
	s.setCreated(big.Name, "2026-03-01")
	// 3000 字那条装不下（跳过），100 字那条仍然入选 —— 不是"遇到大的就停"
	out := s.Render(200)
	if !strings.Contains(out, "长长长") {
		t.Errorf("小条目该入选：\n%s", out)
	}
	if strings.Contains(out, "新新新") {
		t.Errorf("超预算的条目不该进提示词：\n%s", out)
	}
	// 预算小到谁都装不下 → 空串（而不是硬塞一条）
	if got := s.Render(20); got != "" {
		t.Errorf("预算不够时应为空，得到 %q", got)
	}
}

func (s *Store) setCreated(name, date string) {
	sc, f, err := s.find(name)
	if err != nil {
		return
	}
	f[FCreated] = date
	body := ""
	p := filepath.Join(s.dir(sc), name+".md")
	if b, err := os.ReadFile(p); err == nil {
		_, body = parse(string(b))
	}
	_ = os.WriteFile(p, []byte(format(f, body)), 0o644)
}
