package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"xaihi/internal/config"
	"xaihi/internal/hook"
	"xaihi/internal/store"
	"xaihi/internal/todo"
)

func TestBashOK(t *testing.T) {
	spec := Registry()["bash"]
	out, err := spec.Run(context.Background(), map[string]any{"command": "echo hello; exit 3"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := out.(string)
	if !strings.Contains(s, "[exit 3]") || !strings.Contains(s, "hello") {
		t.Fatalf("输出不对: %q", s)
	}
}

func TestBashTimeoutKillsGroup(t *testing.T) {
	spec := Registry()["bash"]
	out, err := spec.Run(context.Background(), map[string]any{"command": "sleep 30; echo never", "timeout": float64(5)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := out.(string)
	if !strings.Contains(s, "[timeout]") {
		t.Fatalf("应超时: %q", s)
	}
	if strings.Contains(s, "never") {
		t.Fatalf("超时后不应有输出: %q", s)
	}
}

func conv() *Env {
	msgs := []store.Message{
		{Seq: 1, Role: "user", Content: "生成一个鹈鹕骑自行车的 SVG"},
		{Seq: 2, Role: "assistant", Content: "```svg\n<svg>pelican</svg>\n```"},
		{Seq: 3, Role: "user", Content: "再画一只猫"},
		{Seq: 4, Role: "tool", Content: strings.Repeat("x", 5000)},
	}
	return &Env{Messages: msgs}
}

func TestRecallKeyword(t *testing.T) {
	spec := Registry()["recall"]
	out, err := spec.Run(context.Background(), map[string]any{"query": "鹈鹕 pelican"}, conv())
	if err != nil {
		t.Fatal(err)
	}
	s := out.(string)
	if !strings.Contains(s, "#1") || !strings.Contains(s, "鹈鹕") {
		t.Fatalf("关键词未命中: %q", s)
	}
}

func TestRecallRegex(t *testing.T) {
	spec := Registry()["recall"]
	out, _ := spec.Run(context.Background(), map[string]any{"query": "(鹈鹕|pelican).*SVG", "re": true}, conv())
	s := out.(string)
	if !strings.Contains(s, "#1") {
		t.Fatalf("正则未命中: %q", s)
	}
	if !strings.Contains(s, "#3") {
		// 第 3 条是"再画一只猫"，不该命中
	} else {
		t.Fatalf("正则误命中: %q", s)
	}
}

func TestRecallBadRegex(t *testing.T) {
	spec := Registry()["recall"]
	out, _ := spec.Run(context.Background(), map[string]any{"query": "(未闭合", "re": true}, conv())
	if !strings.Contains(out.(string), "正则编译失败") {
		t.Fatalf("应报正则错误: %q", out)
	}
}

func TestRecallAround(t *testing.T) {
	spec := Registry()["recall"]
	out, _ := spec.Run(context.Background(), map[string]any{"around": float64(2)}, conv())
	s := out.(string)
	if !strings.Contains(s, "#1") || !strings.Contains(s, "#3") {
		t.Fatalf("around 2 应包含 #1..#3: %q", s)
	}
	if !strings.Contains(s, "#4") {
		t.Fatalf("around 2（±2）应含 #4: %q", s)
	}
}

func TestRecallNoEnv(t *testing.T) {
	spec := Registry()["recall"]
	out, _ := spec.Run(context.Background(), map[string]any{"query": "x"}, nil)
	if !strings.Contains(out.(string), "没有会话上下文") {
		t.Fatalf("无上下文应明确报错: %q", out)
	}
}

func TestRecallMiss(t *testing.T) {
	spec := Registry()["recall"]
	out, _ := spec.Run(context.Background(), map[string]any{"query": "绝不存在的词"}, conv())
	if !strings.Contains(out.(string), "没有命中") {
		t.Fatalf("未命中应明确提示: %q", out)
	}
}

// 钩子工具：真的落盘、绑当前会话、能被 list 读回、能删
func TestHookTools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XAIHI_DATA_DIR", dir)
	env := &Env{ConvID: 77}
	out, err := runHookAdd(context.Background(), map[string]any{
		"name": "steam-guard", "prompt": "盯着 steam，玩超过 90 分钟提醒我", "quota": 5.0,
	}, env)
	if err != nil {
		t.Fatalf("hook_add: %v", err)
	}
	if s, _ := out.(string); !strings.Contains(s, "steam-guard") || !strings.Contains(s, "#77") {
		t.Fatalf("返回不对: %v", out)
	}
	defs, _ := hook.LoadDir(filepath.Join(dir, "hooks"))
	if len(defs) != 1 || defs[0].ConvID != 77 || defs[0].QuotaPerDay != 5 {
		t.Fatalf("落盘不对: %+v", defs)
	}
	out, _ = runHookList(context.Background(), nil, env)
	if !strings.Contains(out.(string), "steam-guard") || !strings.Contains(out.(string), "#77") {
		t.Fatalf("list 不对: %v", out)
	}
	if out, _ := runHookRemove(context.Background(), map[string]any{"name": "steam-guard"}, env); !strings.Contains(out.(string), "已删除") {
		t.Fatalf("rm 不对: %v", out)
	}
	if defs, _ := hook.LoadDir(filepath.Join(dir, "hooks")); len(defs) != 0 {
		t.Fatalf("删完还有: %+v", defs)
	}
	// 非法名字要被拒（路径穿越）
	if out, _ := runHookAdd(context.Background(), map[string]any{"name": "../x", "prompt": "p"}, env); !strings.Contains(out.(string), "✗") {
		t.Fatalf("非法名字应该被拒: %v", out)
	}
}

// 待办工具：写入、列出、完成
func TestTodoTools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XAIHI_DATA_DIR", dir)
	env := &Env{ConvID: 9}

	out, err := runTodoAdd(context.Background(), map[string]any{
		"what": "交周报", "at": "2026-11-11 09:00",
	}, env)
	if err != nil || !strings.Contains(out.(string), "已记下待办") {
		t.Fatalf("todo_add = %v, %v", out, err)
	}
	list, _ := runTodoList(context.Background(), nil, env)
	if !strings.Contains(list.(string), "交周报") || !strings.Contains(list.(string), "11-11 09:00") {
		t.Fatalf("todo_list = %v", list)
	}
	// 取 id 标完成
	items, _ := todo.Open(config.DataPath("todos.json")).List()
	if len(items) != 1 || items[0].ConvID != 9 {
		t.Fatalf("落盘不对: %+v", items)
	}
	done, _ := runTodoDone(context.Background(), map[string]any{"id": items[0].ID}, env)
	if !strings.Contains(done.(string), "已标记完成") {
		t.Fatalf("todo_done = %v", done)
	}
	if pend, _ := todo.Open(config.DataPath("todos.json")).Pending(0); len(pend) != 0 {
		t.Errorf("完成之后不该还在 pending: %+v", pend)
	}
	// 空内容
	if out, _ := runTodoAdd(context.Background(), map[string]any{"what": " "}, env); !strings.Contains(out.(string), "✗") {
		t.Errorf("空内容应该被拒: %v", out)
	}
	// 时间看不懂
	if out, _ := runTodoAdd(context.Background(), map[string]any{"what": "x", "at": "明天"}, env); !strings.Contains(out.(string), "✗") {
		t.Errorf("非法时间应该被拒: %v", out)
	}
}
