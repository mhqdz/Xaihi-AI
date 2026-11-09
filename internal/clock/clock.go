// Package clock：给她"时间感"。
//
// 陪伴型 agent 最常犯的错是不知道现在几点：模型会说"该休息了"，但它不知道
// 现在是 04:40 还是 14:40；会说"你这几天很忙"，但它不知道今天到底动没动。
// 这几行就是补这个 —— 很短、每轮都注入，所以必须控制在 200 字内。
package clock

import (
	"fmt"
	"strings"
	"time"

	"xaihi/internal/watch"
)

var weekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// Now 人读的当前时间：2026-11-10 04:40 周二
func Now(t time.Time) string {
	return t.Format("2006-01-02 15:04") + " " + weekdays[int(t.Weekday())]
}

// Ago 相对时间：刚刚 / 12 分钟前 / 3 小时前 / 2 天前
func Ago(d time.Duration) string {
	switch {
	case d < 0:
		return "刚刚"
	case d < 90*time.Second:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

// span 人读的时长：1h20m / 45m
func span(d time.Duration) string {
	if d < time.Minute {
		return "不到 1 分钟"
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

// Today 今天在电脑前多久、切过哪些应用、现在在用啥。
// 事件来自 watch.db（观察器只记"稳定切换"和"关窗"，所以这里是粗粒度）。
func Today(now time.Time, events []watch.Event) string {
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var today []watch.Event
	for _, e := range events {
		if !e.At.Before(dayStart) && !e.At.After(now) {
			today = append(today, e)
		}
	}
	if len(today) == 0 {
		// 今天没有事件：看最近一次活动是什么时候
		if len(events) > 0 {
			last := events[len(events)-1]
			return "今天还没有活动记录；上次动电脑是 " + Ago(now.Sub(last.At)) + "（" + last.App + "）"
		}
		return "今天还没有活动记录"
	}

	first, last := today[0], today[len(today)-1]
	switches := 0
	apps := map[string]bool{}
	for _, e := range today {
		if e.Kind == watch.KindSwitch {
			switches++
			if e.App != "" {
				apps[e.App] = true
			}
		}
	}

	var b strings.Builder
	// 还在用（最后事件 10 分钟内）才说"在电脑前"，否则说"最后活跃"
	if now.Sub(last.At) <= 15*time.Minute {
		fmt.Fprintf(&b, "在电脑前 %s", span(now.Sub(first.At)))
		if last.App != "" {
			fmt.Fprintf(&b, "，当前 %s", last.App)
		}
	} else {
		fmt.Fprintf(&b, "上次活动 %s（%s）", Ago(now.Sub(last.At)), last.App)
	}
	if switches > 0 {
		fmt.Fprintf(&b, "；今天切过 %d 次应用", switches)
		if len(apps) > 1 {
			names := make([]string, 0, len(apps))
			for a := range apps {
				names = append(names, a)
			}
			sortStrings(names)
			if len(names) > 4 {
				names = append(names[:4], "…")
			}
			b.WriteString("（" + strings.Join(names, "、") + "）")
		}
	}
	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Render 拼成注入用的那几行。lastUser 为零值时省略"上次对话"。
func Render(now time.Time, events []watch.Event, lastUser time.Time) string {
	var b strings.Builder
	b.WriteString("[现在] " + Now(now) + "\n")
	b.WriteString("[今天] " + Today(now, events))
	if !lastUser.IsZero() {
		b.WriteString("\n[上次对话] " + Ago(now.Sub(lastUser)))
	}
	return b.String()
}
