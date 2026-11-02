package store

import "testing"

// 不存在的 id 必须报错。
//
// Archive / Restore / Delete 原来只看 Exec 返回的 err，而 UPDATE/DELETE 命中 0 行
// 并不算错误 —— 于是 HTTP 层会回 {"ok":true,"archived":"999999"}，等于告诉客户端
// 「归档成功了」，实际库里什么都没变。同样的问题在 Restore 和 Delete 上都有。
func TestArchiveRestoreDeleteMissingID(t *testing.T) {
	s := newSQ(t)
	const missing = "999999"

	if err := s.Archive(missing); err == nil {
		t.Error("Archive 不存在的会话应该报错")
	}
	if err := s.Restore(missing); err == nil {
		t.Error("Restore 不存在的会话应该报错")
	}
	if err := s.Delete(missing, false); err == nil {
		t.Error("Delete(软删) 不存在的会话应该报错")
	}
	if err := s.Delete(missing, true); err == nil {
		t.Error("Delete(硬删) 不存在的会话应该报错")
	}

	// 存在的会话必须照常成功 —— 别为了判不存在把正常路径也弄坏
	c := &Conversation{Title: "有"}
	if err := s.Save(c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Archive(c.ID); err != nil {
		t.Errorf("Archive 存在的会话不该报错：%v", err)
	}
	if err := s.Restore(c.ID); err != nil {
		t.Errorf("Restore 存在的会话不该报错：%v", err)
	}
	if err := s.Delete(c.ID, true); err != nil {
		t.Errorf("Delete 存在的会话不该报错：%v", err)
	}
	if _, err := s.Get(c.ID); err == nil {
		t.Error("硬删之后不该还能读到")
	}
}
