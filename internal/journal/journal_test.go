package journal

import (
	"strings"
	"testing"
	"time"
)

var day = time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local)

func newStore(t *testing.T) *Store { return &Store{Dir: t.TempDir()} }

func TestAppendAndRead(t *testing.T) {
	s := newStore(t)
	if err := s.Append(day, day.Add(5*time.Hour+12*time.Minute), "他今天在调 TTS。"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append(day, day.Add(23*time.Hour), "他说还是卡在 ASR 上。"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read(day)
	if !strings.Contains(got, "# 2026-11-10 周二") || !strings.Contains(got, "## 05:12") ||
		!strings.Contains(got, "## 23:00") {
		t.Errorf("格式不对:\n%s", got)
	}
	if !strings.Contains(got, "他今天在调 TTS。") || !strings.Contains(got, "他说还是卡在 ASR 上。") {
		t.Errorf("内容丢了:\n%s", got)
	}
	// 没有的日期是空串，不是错误
	if s2, err := s.Read(day.AddDate(0, 0, 1)); err != nil || s2 != "" {
		t.Errorf("空日期 = %q, %v", s2, err)
	}
}

func TestMaxRunes(t *testing.T) {
	s := newStore(t)
	long := strings.Repeat("字", MaxRunes+1)
	if err := s.Append(day, day, long); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超长应该被拒: %v", err)
	}
	if got, _ := s.Read(day); got != "" {
		t.Errorf("被拒时不该写文件: %q", got)
	}
	// 刚好 500 可以
	if err := s.Append(day, day, strings.Repeat("字", MaxRunes)); err != nil {
		t.Fatalf("刚好 500 应该允许: %v", err)
	}
}

func TestDays(t *testing.T) {
	s := newStore(t)
	s.Append(day, day, "a")
	s.Append(day.AddDate(0, 0, -2), day, "b")
	days, _ := s.Days()
	if len(days) != 2 || days[0] != "2026-11-08" || days[1] != "2026-11-10" {
		t.Errorf("Days = %v", days)
	}
}

func TestHint(t *testing.T) {
	s := newStore(t)
	h := s.Hint()
	if !strings.Contains(h, "[日记]") || !strings.Contains(h, s.Dir) {
		t.Errorf("Hint = %q", h)
	}
	if strings.Contains(h, "\n") {
		t.Errorf("Hint 应该只有一行: %q", h)
	}
}

func TestEmptyAppend(t *testing.T) {
	s := newStore(t)
	if err := s.Append(day, day, "  "); err == nil {
		t.Error("空内容应该被拒")
	}
}
