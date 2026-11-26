package wiki

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestWikiCRUDSearchAndKBLinks(t *testing.T) {
	s := New(t.TempDir())
	created, err := s.Write("index", "# 索引\n\n工作入口", []string{"0100", "0004", "0100"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision == "" || strings.Join(created.KBLinks, ",") != "0004,0100" {
		t.Errorf("创建结果不对：%+v", created)
	}
	read, err := s.Read("index")
	if err != nil || read.Body != "# 索引\n\n工作入口" || read.Revision != created.Revision {
		t.Fatalf("读回不对：%+v, %v", read, err)
	}
	pages, err := s.Search("0004", 10)
	if err != nil || len(pages) != 1 {
		t.Errorf("kb id 搜索不对：%+v, %v", pages, err)
	}
	if _, err := s.Write("index", "无版本覆盖", nil, ""); err == nil {
		t.Error("更新已存在页面必须带 revision")
	}
	if _, err := s.Write("index", "过期覆盖", nil, "bad"); !errors.Is(err, ErrRevisionConflict) {
		t.Errorf("过期 revision 应该冲突：%v", err)
	}
	updated, err := s.Write("index", "新正文", []string{"010200"}, created.Revision)
	if err != nil || updated.Body != "新正文" || updated.Revision == created.Revision {
		t.Fatalf("正常更新失败：%+v, %v", updated, err)
	}
	listed, err := s.List()
	if err != nil || len(listed) != 1 || listed[0].Body != "新正文" {
		t.Errorf("List 不对：%+v, %v", listed, err)
	}
	if err := s.Delete("index", created.Revision); !errors.Is(err, ErrRevisionConflict) {
		t.Errorf("过期 revision 不该删除：%v", err)
	}
	if err := s.Delete("index", updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("index"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后应返回 ErrNotFound：%v", err)
	}
}

func TestWikiValidation(t *testing.T) {
	s := New(t.TempDir())
	for _, name := range []string{"../evil", "A", "a/b", ""} {
		if _, err := s.Write(name, "x", nil, ""); err == nil {
			t.Errorf("非法页面名 %q 应该被拒绝", name)
		}
	}
	if _, err := s.Write("index", "x", []string{"../kb"}, ""); err == nil {
		t.Error("非法 kb id 应该被拒绝")
	}
	if _, err := s.Write("index", "", nil, ""); err == nil {
		t.Error("空正文应该被拒绝")
	}
	if _, err := s.Write("index", strings.Repeat("字", MaxBodyRunes+1), nil, ""); err == nil {
		t.Error("超长正文应该被拒绝")
	}
	if err := s.Delete("index", ""); err == nil {
		t.Error("空 revision 不该允许删除")
	}
}

func TestWikiConcurrentUpdateOnlyOneSucceeds(t *testing.T) {
	root := t.TempDir()
	base := New(root)
	old, err := base.Write("index", "旧", nil, "")
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
			_, err := s.Write("index", fmt.Sprintf("新 %d", i), nil, old.Revision)
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
}

func TestWikiOwnerRootsAreDistinct(t *testing.T) {
	dataDir := t.TempDir()
	a := New(OwnerRoot(dataDir, "work"))
	b := New(OwnerRoot(dataDir, "life"))
	if _, err := a.Write("private", "工作秘密", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Read("private"); !errors.Is(err, ErrNotFound) {
		t.Errorf("另一个人格不该读到页面：%v", err)
	}
	if _, err := os.Stat(OwnerRoot(dataDir, "work") + "/private.md"); err != nil {
		t.Errorf("页面没有落在 owner 目录：%v", err)
	}
}
