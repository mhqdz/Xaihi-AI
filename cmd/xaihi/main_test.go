package main

import "testing"

func TestParseDreamTime(t *testing.T) {
	for _, value := range []string{"2026-11-19", "2026-11-19T12:30:00Z"} {
		if _, err := parseDreamTime(value); err != nil {
			t.Errorf("parseDreamTime(%q): %v", value, err)
		}
	}
	if _, err := parseDreamTime("昨天"); err == nil {
		t.Error("模糊时间应明确拒绝，避免 dreaming 范围猜错")
	}
}
