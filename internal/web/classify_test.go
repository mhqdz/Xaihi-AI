package web

import (
	"testing"

	"agentd/internal/store"
)

// classifyMessage 是「前端只认 kind/status、不再拿正则猜正文」的唯一判定点，
// 每个分支都钉一遍，免得以后再往里塞新状态时悄悄改掉旧语义。
func TestClassifyMessage(t *testing.T) {
	mk := func(role, content, reasoning, status, origin string) *store.Message {
		return &store.Message{Role: role, Content: content, Reasoning: reasoning, Status: status, Origin: origin}
	}
	cases := []struct {
		name string
		m    *store.Message
		want string
	}{
		{"普通正文", mk("assistant", "好的", "", "done", ""), ""},
		{"历史数据（status 空）", mk("assistant", "旧消息", "", "", ""), ""},
		{"正在流式", mk("assistant", "半句话", "", "running", ""), "streaming"},
		{"有正文的中断", mk("assistant", "说到一半", "", "interrupted", ""), "interrupted"},
		{"只有思考的中断也算 interrupted", mk("assistant", "", "想了半天", "interrupted", ""), "interrupted"},
		{"空壳中断当系统提示", mk("assistant", "（已停止）", "", "interrupted", ""), "sysnote"},
		{"上游报错说明当系统提示", mk("assistant", "（中断：connection reset）", "", "failed", ""), "sysnote"},
		{"timer 来源", mk("assistant", "（任务回报）", "", "done", "timer"), "sysnote"},
		{"handoff 来源", mk("assistant", "交接内容", "", "done", "handoff"), "sysnote"},
		{"旧数据里的轮次上限提示", mk("assistant", "——（已达到工具轮次上限）", "", "", ""), "sysnote"},
	}
	for _, c := range cases {
		if got := classifyMessage(c.m); got != c.want {
			t.Errorf("%s: classifyMessage() = %q, 期望 %q", c.name, got, c.want)
		}
	}
}

// 旧库里的系统提示是直接写在正文里的，正则必须只认「开头的整段括号」，
// 否则用户自己写一句“前半（已停止）”也会被当成系统提示。
func TestLegacySysNote(t *testing.T) {
	yes := []string{
		"（已停止）",
		"（中断：broken pipe）",
		"——（已达到工具轮次上限）",
		"（检测到连续 3 次空转）",
		"（任务回报）",
		"（任务已派发）",
		"（进程中断）",
		"   （已停止）",
	}
	for _, s := range yes {
		if !legacySysNote.MatchString(s) {
			t.Errorf("%q 应该被识别为系统提示", s)
		}
	}
	no := []string{
		"正常回答",
		"（这是普通括号）",
		"已停止",
		"前半（已停止）",   // 前面有正文，不是独立提示
		"我（已停止）了",   // 出现在句中
	}
	for _, s := range no {
		if legacySysNote.MatchString(s) {
			t.Errorf("%q 不该被识别为系统提示", s)
		}
	}
}

// 工具截图占位消息不是一次用户提问，统计轮次时必须排除，
// 否则标尺会多出一格、标题会显示占位文案。
func TestIsShotPlaceholder(t *testing.T) {
	yes := &store.Message{Role: "user", Content: "（上一个工具的截图，请以图为准）", Images: []string{"data:image/png;base64,xx"}}
	if !isShotPlaceholder(yes) {
		t.Error("带图 + 固定前缀的占位消息应被识别")
	}
	no := []*store.Message{
		{Role: "user", Content: "（上一个工具的截图，请以图为准）"},                    // 没图就不算
		{Role: "user", Content: "正常提问", Images: []string{"data:image/png;base64,xx"}}, // 有图但没前缀
		{Role: "assistant", Content: "（上一个工具的截图，请以图为准）", Images: []string{"x"}},
	}
	for i, m := range no {
		if isShotPlaceholder(m) {
			t.Errorf("第 %d 条不该被识别为占位消息", i)
		}
	}
}

func conv3() []store.Message {
	return []store.Message{
		{Seq: 1, Role: "user", Content: "q1"},
		{Seq: 2, Role: "assistant", Content: "a1"},
		{Seq: 3, Role: "user", Content: "q2"},
		{Seq: 4, Role: "assistant", Content: "a2"},
		{Seq: 5, Role: "user", Content: "q3"},
		{Seq: 6, Role: "assistant", Content: "a3"},
	}
}

func TestConvWindowPages(t *testing.T) {
	all := conv3()

	// 轮数足够：一次全给
	out, from, to, total, hasMore := convWindow(all, 10, 0, 0)
	if total != 3 || from != 1 || to != 3 || hasMore || len(out) != 6 {
		t.Errorf("取全部：from=%d to=%d total=%d hasMore=%v len=%d", from, to, total, hasMore, len(out))
	}

	// 只要最后 2 轮：应返回 q2..a3，并且标记上面还有
	out, from, to, total, hasMore = convWindow(all, 2, 0, 0)
	if total != 3 || from != 2 || to != 3 || !hasMore || len(out) != 4 {
		t.Errorf("最后 2 轮：from=%d to=%d total=%d hasMore=%v len=%d", from, to, total, hasMore, len(out))
	}
	if out[0].Seq != 3 || out[len(out)-1].Seq != 6 {
		t.Errorf("最后 2 轮的 seq 范围应 3..6，实际 %d..%d", out[0].Seq, out[len(out)-1].Seq)
	}

	// before：取 seq < 5 的第 2 轮
	out, from, to, _, _ = convWindow(all, 2, 5, 0)
	if from != 1 || to != 2 || len(out) != 4 || out[0].Seq != 1 {
		t.Errorf("before=5 取 2 轮：from=%d to=%d len=%d first=%d", from, to, len(out), out[0].Seq)
	}

	// before 落在第一轮之前：没有可给的
	out, from, to, total, hasMore = convWindow(all, 2, 1, 0)
	if out != nil || from != 0 || to != 0 || total != 3 || hasMore {
		t.Errorf("before 越界应返回空：out=%v from=%d to=%d total=%d", out, from, to, total)
	}

	// around：跳第 3 轮
	out, from, to, _, _ = convWindow(all, 10, 0, 3)
	if from != 1 || to != 3 || len(out) != 6 {
		t.Errorf("around=3：from=%d to=%d len=%d", from, to, len(out))
	}

	// 空会话
	if out, from, to, total, hasMore := convWindow(nil, 10, 0, 0); out != nil || from != 0 || to != 0 || total != 0 || hasMore {
		t.Error("空会话应返回全零")
	}
}

// 截图占位消息不能算一轮 —— 否则标尺多一格、total_rounds 偏大。
func TestConvWindowIgnoresShotPlaceholder(t *testing.T) {
	all := []store.Message{
		{Seq: 1, Role: "user", Content: "q1"},
		{Seq: 2, Role: "assistant", Content: "a1"},
		{Seq: 3, Role: "user", Content: "（上一个工具的截图，请以图为准）", Images: []string{"data:image/png;base64,xx"}},
		{Seq: 4, Role: "assistant", Content: "看了图"},
	}
	_, _, _, total, _ := convWindow(all, 10, 0, 0)
	if total != 1 {
		t.Errorf("占位消息不该算一轮：total=%d，期望 1", total)
	}
}
