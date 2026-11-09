package clock

import (
	"strings"
	"testing"
	"time"

	"xaihi/internal/watch"
)

func at(h, m int) time.Time {
	return time.Date(2026, 11, 10, h, m, 0, 0, time.Local)
}

func TestNow(t *testing.T) {
	if got := Now(at(4, 40)); got != "2026-11-10 04:40 周二" {
		t.Errorf("Now = %q", got)
	}
}

func TestAgo(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Second: "刚刚",
		5 * time.Minute:  "5 分钟前",
		3 * time.Hour:    "3 小时前",
		50 * time.Hour:   "2 天前",
		-5 * time.Minute: "刚刚",
	}
	for d, want := range cases {
		if got := Ago(d); got != want {
			t.Errorf("Ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTodayActive(t *testing.T) {
	ev := []watch.Event{
		{At: at(2, 0), Kind: watch.KindSwitch, App: "firefox"},
		{At: at(3, 0), Kind: watch.KindSwitch, App: "kitty"},
		{At: at(4, 30), Kind: watch.KindSwitch, App: "firefox"},
	}
	got := Today(at(4, 40), ev)
	if !strings.Contains(got, "在电脑前 2h40m") || !strings.Contains(got, "当前 firefox") {
		t.Errorf("Today = %q", got)
	}
	if !strings.Contains(got, "切过 3 次") {
		t.Errorf("应该统计切换次数: %q", got)
	}
}

// 最后事件很久以前：不说"在电脑前"，说"上次活动"
func TestTodayIdle(t *testing.T) {
	ev := []watch.Event{{At: at(1, 0), Kind: watch.KindSwitch, App: "firefox"}}
	got := Today(at(9, 0), ev)
	if !strings.Contains(got, "上次活动 8 小时前") {
		t.Errorf("Today = %q", got)
	}
	if strings.Contains(got, "在电脑前") {
		t.Errorf("不该说在电脑前: %q", got)
	}
}

// 今天没有事件但昨天有
func TestTodayEmpty(t *testing.T) {
	ev := []watch.Event{{At: time.Date(2026, 11, 9, 22, 0, 0, 0, time.Local), Kind: watch.KindSwitch, App: "firefox"}}
	got := Today(at(9, 0), ev)
	if !strings.Contains(got, "今天还没有活动记录") || !strings.Contains(got, "上次动电脑") {
		t.Errorf("Today = %q", got)
	}
}

func TestRender(t *testing.T) {
	ev := []watch.Event{{At: at(4, 30), Kind: watch.KindSwitch, App: "firefox"}}
	got := Render(at(4, 40), ev, at(1, 40))
	if !strings.Contains(got, "[现在]") || !strings.Contains(got, "[今天]") || !strings.Contains(got, "[上次对话] 3 小时前") {
		t.Errorf("Render = %q", got)
	}
	// 没有上次对话时不该有第三行
	if s := Render(at(4, 40), nil, time.Time{}); strings.Contains(s, "[上次对话]") {
		t.Errorf("零值不该输出上次对话: %q", s)
	}
}
