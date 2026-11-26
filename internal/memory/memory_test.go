package memory

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir())
	s.Scope = "xaihi"
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
		if e.Field(FScope) != "xaihi" {
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
	if _, err := s.Save("xaihi", "事实", "", "项目的"); err != nil {
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
	if _, err := os.Stat(filepath.Join(s.Root, "project", "xaihi", e.Name+".md")); err != nil {
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
	if _, err := os.Stat(filepath.Join(s.Root, "project", "xaihi", e.Name+".md")); !os.IsNotExist(err) {
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
	big, err := s.Save("", "偏好", "", strings.Repeat("新", 300))
	if err != nil {
		t.Fatalf("保存预算内允许的大条目失败：%v", err)
	}
	s.setCreated(big.Name, "2026-03-01")
	// 300 字那条装不下（跳过），100 字那条仍然入选 —— 不是"遇到大的就停"
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

func TestSaveWithBasisAndSource(t *testing.T) {
	s := newStore(t)
	e, err := s.SaveWithBasis("", "经验", "conv:1469#42", BasisVerified, "已经验证过的规则")
	if err != nil {
		t.Fatal(err)
	}
	if e.Revision == "" || len(e.Revision) != 64 {
		t.Fatalf("revision 应该是 SHA-256：%q", e.Revision)
	}
	got, _ := s.List()
	if len(got) != 1 {
		t.Fatalf("List = %d 条", len(got))
	}
	if got[0].Field(FSource) != "conv:1469#42" || got[0].Field(FBasis) != BasisVerified {
		t.Errorf("来源或依据丢失：%+v", got[0].Fields)
	}
	if got[0].Revision != e.Revision {
		t.Errorf("落盘前后 revision 不一致：%q != %q", got[0].Revision, e.Revision)
	}
	if _, err := s.SaveWithBasis("", "经验", "", "made-up", "不应落盘"); err == nil {
		t.Error("非法 basis 应该被拒绝")
	}
}

func TestStatusChangePreservesUnknownFields(t *testing.T) {
	s := newStore(t)
	e, _ := s.Save("", "偏好", "", "原正文")
	p := filepath.Join(s.dir("xaihi"), e.Name+".md")
	r, err := s.find(e.Name)
	if err != nil {
		t.Fatal(err)
	}
	r.Fields["x-custom"] = "人工字段"
	r.Fields["x-empty"] = ""
	if err := writeAtomic(p, []byte(format(r.Fields, r.Body)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget(e.Name); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fields, body := parse(string(raw))
	if fields["x-custom"] != "人工字段" {
		t.Errorf("状态变更丢了未知字段：%q", string(raw))
	}
	if _, ok := fields["x-empty"]; !ok {
		t.Errorf("状态变更丢了空值字段：%q", string(raw))
	}
	if fields[FUpdated] == "" || fields[FStatus] != StatusArchived || strings.TrimSpace(body) != "原正文" {
		t.Errorf("状态、更新时间或正文不对：%q", string(raw))
	}
}

func TestReplaceCreatesVersionChain(t *testing.T) {
	s := newStore(t)
	old, _ := s.SaveWithBasis("", "偏好", "conv:1#1", BasisExplicit, "旧说法")
	fresh, err := s.Replace(old.Name, old.Revision, "", "conv:2#9", BasisVerified, "新说法")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if fresh.Name == old.Name || fresh.Field(FSupersedes) != old.Name {
		t.Errorf("新版本链不对：%+v", fresh)
	}
	oldAfter, err := s.find(old.Name)
	if err != nil {
		t.Fatal(err)
	}
	if oldAfter.Fields[FStatus] != StatusSuperseded || oldAfter.Fields[FSupersededBy] != fresh.Name {
		t.Errorf("旧版本链不对：%+v", oldAfter.Fields)
	}
	if oldAfter.Body != "旧说法" || oldAfter.Fields[FUpdated] == "" {
		t.Errorf("旧正文被覆盖或缺少 updated：%+v", oldAfter)
	}
	active, _ := s.List()
	if len(active) != 1 || active[0].Name != fresh.Name || active[0].Body != "新说法" {
		t.Errorf("只有新版本应该 active：%+v", active)
	}
	if active[0].Field(FSource) != "conv:2#9" || active[0].Field(FBasis) != BasisVerified {
		t.Errorf("新版本证据不对：%+v", active[0].Fields)
	}
}

func TestReplaceRejectsStaleRevision(t *testing.T) {
	s := newStore(t)
	old, _ := s.Save("", "偏好", "", "旧说法")
	p := filepath.Join(s.dir("xaihi"), old.Name+".md")
	if err := writeAtomic(p, []byte(format(old.Fields, "人工并发修改")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Replace(old.Name, old.Revision, "", "conv:2#9", "", "模型覆盖"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("过期 revision 应该报冲突，得到 %v", err)
	}
	active, _ := s.List()
	if len(active) != 1 || active[0].Body != "人工并发修改" {
		t.Errorf("冲突时不该覆盖人工修改：%+v", active)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("冲突时不该留下替换文件或临时文件：%+v", entries)
	}
}

func TestReplaceRequiresActiveEntryAndRevision(t *testing.T) {
	s := newStore(t)
	e, _ := s.Save("", "偏好", "", "旧说法")
	if _, err := s.Replace(e.Name, "", "", "", "", "新说法"); err == nil {
		t.Error("空 revision 应该被拒绝")
	}
	if err := s.Forget(e.Name); err != nil {
		t.Fatal(err)
	}
	r, _ := s.find(e.Name)
	if _, err := s.Replace(e.Name, r.Revision, "", "", "", "新说法"); err == nil || strings.Contains(err.Error(), "版本冲突") {
		t.Errorf("archived 应该明确拒绝替换，而不是版本冲突：%v", err)
	}
}

func TestConcurrentSaveAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := New(root)
			s.Scope = "xaihi"
			_, err := s.Save("", "经历", fmt.Sprintf("conv:%d#1", i+1), fmt.Sprintf("记忆 %d", i))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("并发 Save: %v", err)
		}
	}
	s := New(root)
	s.Scope = "xaihi"
	all, err := s.List()
	if err != nil || len(all) != n {
		t.Fatalf("并发写入后应完整读到 %d 条，得到 %d：%v", n, len(all), err)
	}
	seen := map[string]bool{}
	for _, e := range all {
		if seen[e.Name] {
			t.Errorf("重复 id：%s", e.Name)
		}
		seen[e.Name] = true
	}
	tmp, err := filepath.Glob(filepath.Join(root, "project", "xaihi", ".*.tmp-*"))
	if err != nil || len(tmp) != 0 {
		t.Errorf("不该残留临时文件：%v, %v", tmp, err)
	}
}

func TestConcurrentReplaceOnlyOneSucceeds(t *testing.T) {
	root := t.TempDir()
	base := New(root)
	base.Scope = "xaihi"
	old, err := base.Save("", "偏好", "", "旧说法")
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := New(root)
			s.Scope = "xaihi"
			_, err := s.Replace(old.Name, old.Revision, "", fmt.Sprintf("conv:%d#2", i+1), "", fmt.Sprintf("替换 %d", i))
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrRevisionConflict):
			conflicts++
		default:
			t.Errorf("意外错误：%v", err)
		}
	}
	if success != 1 || conflicts != n-1 {
		t.Errorf("同一 revision 只能成功一次：success=%d conflicts=%d", success, conflicts)
	}
	active, _ := base.List()
	if len(active) != 1 || active[0].Name == old.Name {
		t.Errorf("最终应该只有一个新 active 版本：%+v", active)
	}
}

// TestMemoryProcessHelper 是真正的跨进程竞争测试入口；普通 go test 中直接返回。
func TestMemoryProcessHelper(t *testing.T) {
	if os.Getenv("XAIHI_MEMORY_TEST_HELPER") != "replace" {
		return
	}
	s := New(os.Getenv("XAIHI_MEMORY_TEST_ROOT"))
	s.Scope = "xaihi"
	_, err := s.Replace(
		os.Getenv("XAIHI_MEMORY_TEST_NAME"), os.Getenv("XAIHI_MEMORY_TEST_REVISION"),
		"", "conv:99#2", "", "来自 "+os.Getenv("XAIHI_MEMORY_TEST_INDEX"),
	)
	if errors.Is(err, ErrRevisionConflict) {
		os.Exit(23)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func TestCrossProcessConcurrentReplace(t *testing.T) {
	if testing.Short() {
		t.Skip("跨进程测试")
	}
	root := t.TempDir()
	base := New(root)
	base.Scope = "xaihi"
	old, err := base.Save("", "偏好", "", "旧说法")
	if err != nil {
		t.Fatal(err)
	}
	const n = 6
	cmds := make([]*exec.Cmd, n)
	outputs := make([]bytes.Buffer, n)
	for i := 0; i < n; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMemoryProcessHelper$")
		cmd.Env = append(os.Environ(),
			"XAIHI_MEMORY_TEST_HELPER=replace",
			"XAIHI_MEMORY_TEST_ROOT="+root,
			"XAIHI_MEMORY_TEST_NAME="+old.Name,
			"XAIHI_MEMORY_TEST_REVISION="+old.Revision,
			fmt.Sprintf("XAIHI_MEMORY_TEST_INDEX=%d", i),
		)
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = cmd
	}
	success, conflicts := 0, 0
	for i, cmd := range cmds {
		err := cmd.Wait()
		switch {
		case err == nil:
			success++
		case func() bool {
			var exitErr *exec.ExitError
			return errors.As(err, &exitErr) && exitErr.ExitCode() == 23
		}():
			conflicts++
		default:
			t.Errorf("子进程 %d 意外失败：%v\n%s", i, err, outputs[i].String())
		}
	}
	if success != 1 || conflicts != n-1 {
		t.Errorf("跨进程同一 revision 只能成功一次：success=%d conflicts=%d", success, conflicts)
	}
	active, _ := base.List()
	if len(active) != 1 || active[0].Name == old.Name {
		t.Errorf("跨进程替换后应该只有一个新 active 版本：%+v", active)
	}
}

func TestWriteNewAtomicDoesNotOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "memory.md")
	if err := os.WriteFile(p, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeNewAtomic(p, []byte("新内容"), 0o644); !os.IsExist(err) {
		t.Fatalf("已存在路径应该返回 exist，得到 %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil || string(raw) != "旧内容" {
		t.Errorf("冲突时旧内容不该被覆盖：%q, %v", raw, err)
	}
}

func TestMigrateLegacyCopiesWithoutOverwriting(t *testing.T) {
	dataDir := t.TempDir()
	legacyRoot := DefaultRoot(dataDir)
	legacy := New(legacyRoot)
	oldGlobal, err := legacy.Save("global", "偏好", "conv:1#1", "旧全局")
	if err != nil {
		t.Fatal(err)
	}
	legacy.Scope = "work"
	if _, err := legacy.Save("work", "经验", "conv:1#2", "旧项目"); err != nil {
		t.Fatal(err)
	}
	root := OwnerRoot(dataDir, "xaihi")
	copied, err := MigrateLegacy(root, legacyRoot)
	if err != nil || copied != 2 {
		t.Fatalf("首次迁移 copied=%d err=%v", copied, err)
	}
	migrated := New(root)
	all, _ := migrated.List()
	if len(all) != 1 || all[0].Name != oldGlobal.Name || all[0].Body != "旧全局" {
		t.Errorf("全局旧记忆没有正确迁移：%+v", all)
	}
	migrated.Scope = "work"
	project, _ := migrated.List()
	if len(project) != 2 {
		t.Errorf("项目旧记忆没有正确迁移：%+v", project)
	}
	// 新目录里的后续修改不能被第二次迁移覆盖，旧目录也仍保留作备份。
	target := filepath.Join(root, "global", oldGlobal.Name+".md")
	if err := writeAtomic(target, []byte("新人格里的修改\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	copied, err = MigrateLegacy(root, legacyRoot)
	if err != nil || copied != 0 {
		t.Errorf("迁移应幂等：copied=%d err=%v", copied, err)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "新人格里的修改\n" {
		t.Errorf("重复迁移覆盖了新修改：%q", raw)
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "global", oldGlobal.Name+".md")); err != nil {
		t.Errorf("旧目录不应被删除：%v", err)
	}
	if _, err := MigrateLegacy(legacyRoot, legacyRoot); err == nil {
		t.Error("新旧根相同应该被拒绝")
	}
}

func TestMemoryNameRejectsTraversal(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"../outside", "../../personas/xaihi", `..\\outside`, "."} {
		if err := s.Forget(name); err == nil || !strings.Contains(err.Error(), "非法记忆名字") {
			t.Errorf("Forget(%q) 应该拒绝路径穿越：%v", name, err)
		}
		if err := s.Purge(name); err == nil || !strings.Contains(err.Error(), "非法记忆名字") {
			t.Errorf("Purge(%q) 应该拒绝路径穿越：%v", name, err)
		}
	}
}

func TestRenderShowsBasis(t *testing.T) {
	s := newStore(t)
	if _, err := s.SaveWithBasis("", "经验", "", BasisInferred, "尚未验证"); err != nil {
		t.Fatal(err)
	}
	if out := s.Render(6000); !strings.Contains(out, "推断") {
		t.Errorf("注入时应该区分模型推断：%q", out)
	}
}

func (s *Store) setCreated(name, date string) {
	r, err := s.find(name)
	if err != nil {
		return
	}
	r.Fields[FCreated] = date
	_ = writeAtomic(r.Path, []byte(format(r.Fields, r.Body)), 0o644)
}
