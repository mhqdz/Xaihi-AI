package todo

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return Open(filepath.Join(t.TempDir(), "todos.json"))
}

var base = time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)

func TestAddListDone(t *testing.T) {
	s := newStore(t)
	it, err := s.Add("交周报", base.Add(2*time.Hour), 7)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if it.ID == "" || it.ConvID != 7 {
		t.Fatalf("返回不对: %+v", it)
	}
	got, _ := s.List()
	if len(got) != 1 || got[0].What != "交周报" {
		t.Fatalf("List = %+v", got)
	}
	if err := s.Done(it.ID); err != nil {
		t.Fatalf("Done: %v", err)
	}
	pend, _ := s.Pending(0)
	if len(pend) != 0 {
		t.Errorf("完成后不该在 pending 里: %+v", pend)
	}
	// 完成的不删，仍在全集里
	if all, _ := s.List(); len(all) != 1 || !all[0].Done {
		t.Errorf("完成记录应该保留: %+v", all)
	}
	if err := s.Done("nope"); err == nil {
		t.Error("不存在的 id 应该报错")
	}
}

func TestEmptyWhat(t *testing.T) {
	s := newStore(t)
	if _, err := s.Add("   ", base, 0); err == nil {
		t.Error("空内容应该被拒")
	}
}

// 到上限报错，而不是挤掉旧的
func TestMaxItems(t *testing.T) {
	s := newStore(t)
	for i := 0; i < MaxItems; i++ {
		if _, err := s.Add("task", base, 0); err != nil {
			t.Fatalf("第 %d 条失败: %v", i+1, err)
		}
	}
	if _, err := s.Add("overflow", base, 0); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超上限应该报错，得到 %v", err)
	}
	if all, _ := s.List(); len(all) != MaxItems {
		t.Errorf("不该挤掉旧的: %d", len(all))
	}
}

// 排序：未完成在前、有时间的按时间升序、没时间的垫底
func TestOrder(t *testing.T) {
	s := newStore(t)
	a, _ := s.Add("C 有 11点", base.Add(2*time.Hour), 0)
	s.Add("A 无时间", time.Time{}, 0)
	b, _ := s.Add("B 有 10点", base.Add(time.Hour), 0)
	_ = s.Done(b.ID) // B 完成 → 排到最后
	got, _ := s.List()
	if len(got) != 3 {
		t.Fatal(len(got))
	}
	if got[0].What != "C 有 11点" || got[1].What != "A 无时间" || got[2].What != "B 有 10点" {
		t.Errorf("顺序不对: %v %v %v", got[0].What, got[1].What, got[2].What)
	}
	_ = a
}

func TestMarkSaid(t *testing.T) {
	s := newStore(t)
	it, _ := s.Add("喝水", base, 0)
	said := base.Add(time.Minute)
	if err := s.MarkSaid(it.ID, said); err != nil {
		t.Fatal(err)
	}
	all, _ := s.List()
	if !all[0].SaidAt.Equal(said) {
		t.Errorf("SaidAt = %v", all[0].SaidAt)
	}
}

func TestRemove(t *testing.T) {
	s := newStore(t)
	it, _ := s.Add("误建", base, 0)
	if err := s.Remove(it.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.List(); len(all) != 0 {
		t.Errorf("应该删掉: %+v", all)
	}
}

func TestRender(t *testing.T) {
	s := newStore(t)
	s.Add("交周报", base.Add(2*time.Hour), 0)
	s.Add("过期的事", base.Add(-time.Hour), 0)
	s.Add("已完成", base, 0)
	// 完成一条
	all, _ := s.List()
	for _, it := range all {
		if it.What == "已完成" {
			s.Done(it.ID)
		}
	}
	out := s.Render(base, 10)
	if !strings.Contains(out, "[待办]") || !strings.Contains(out, "交周报") {
		t.Errorf("Render = %q", out)
	}
	if strings.Contains(out, "已完成") {
		t.Errorf("完成的条目不该出现: %q", out)
	}
	if !strings.Contains(out, "已过") {
		t.Errorf("过期应该标出来: %q", out)
	}
	// limit 生效 + 报剩余
	if small := s.Render(base, 1); !strings.Contains(small, "还有 1 条") {
		t.Errorf("应该提示还有几条: %q", small)
	}
}
