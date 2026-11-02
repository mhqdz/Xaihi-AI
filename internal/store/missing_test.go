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

// Get 必须严格解析 id。以前用 fmt.Sscanf("%d")，它会忽略尾部垃圾 ——
// "51abc" 被当成 51，于是 /api/conv/51abc 原样返回别人的会话（200）。
func TestGetRejectsMalformedID(t *testing.T) {
	s := newSQ(t)
	c := &Conversation{Title: "real"}
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	// 正数本身能读
	if _, err := s.Get(c.ID); err != nil {
		t.Fatalf("正常 id 应该能读：%v", err)
	}
	// 前后空白是被有意容忍的（URL 里偶尔会带），所以不在坏样本里
	if _, err := s.Get("  " + c.ID + " "); err != nil {
		t.Fatalf("前后空白应该被容忍：%v", err)
	}
	for _, bad := range []string{c.ID + "abc", "", "0", "-1", "abc", "1e3", "0x10"} {
		if _, err := s.Get(bad); err == nil {
			t.Errorf("id %q 不该被接受（会被解析成别的会话）", bad)
		}
	}
}
