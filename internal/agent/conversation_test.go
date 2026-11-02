package agent

import (
	"testing"

	"agentd/internal/config"
	"agentd/internal/store"
)

// Conversation 的三种输入必须区分清楚：
//   - 空 id   → 新建
//   - 存在的 id → 续上原会话
//   - 不存在的 id → 报错（不能静默新建）
//
// 第三条是这次修的 bug：原来是读不到就新建一个，前端把 id 拼错时消息会悄悄
// 写进新会话，用户以为还在原会话，还白烧一次模型调用。
func TestConversationResolution(t *testing.T) {
	st, err := store.NewSQLite(t.TempDir())
	if err != nil {
		t.Fatalf("建临时库失败：%v", err)
	}
	a := New(config.Config{}, st, config.Profile{})

	// 空 id → 新建
	c, err := a.Conversation("")
	if err != nil {
		t.Fatalf("空 id 应该新建而不是报错：%v", err)
	}
	if c.Title != "新会话" {
		t.Errorf("新会话标题应为「新会话」，实际 %q", c.Title)
	}
	// SQLiteStore.NextID() 返回空串（ID 由 AUTOINCREMENT 在 INSERT 时分配），
	// 所以 ID 要在 Save 之后才有；HTTP 层也是先 Conversation 再 Save。
	if err := st.Save(c); err != nil {
		t.Fatalf("存新会话失败：%v", err)
	}
	if c.ID == "" {
		t.Fatal("保存后应该拿到由数据库分配的 ID")
	}

	// 存在的 id → 原样返回
	got, err := a.Conversation(c.ID)
	if err != nil {
		t.Fatalf("存在的会话不该报错：%v", err)
	}
	if got.ID != c.ID {
		t.Errorf("ID 不一致：%s vs %s", got.ID, c.ID)
	}

	// 首尾空白也要按同一个 id 处理
	if got, err := a.Conversation("  " + c.ID + "  "); err != nil || got.ID != c.ID {
		t.Errorf("带空白的 id 应被去掉空白后匹配：got=%v err=%v", got, err)
	}

	// 不存在的 id → 必须报错，且不能顺手造出新会话
	before, _ := st.List()
	if _, err := a.Conversation("999999"); err == nil {
		t.Fatal("不存在的 id 不该被静默新建，应该报错")
	}
	after, _ := st.List()
	if len(after) != len(before) {
		t.Errorf("报错路径不该产生新会话：%d -> %d", len(before), len(after))
	}
}
