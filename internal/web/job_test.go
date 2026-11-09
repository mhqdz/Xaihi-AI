package web

import (
	"testing"
	"time"
)

func TestJobTTLAndSweep(t *testing.T) {
	t.Setenv("XAIHI_JOB_TTL", "30s")
	s := &Server{}
	j := s.job("1")
	j.emit("event: delta\ndata: {}\n\n")
	j.finish()

	if jobTTL() != 30*time.Second {
		t.Fatalf("TTL 未生效: %v", jobTTL())
	}
	// 未到期：不应被清掉
	s.sweepJobs()
	if _, ok := s.jobs["1"]; !ok {
		t.Fatalf("未到期就被清理了")
	}
	// 人为把到期时间提前
	j.mu.Lock()
	j.expireAt = time.Now().Add(-time.Second)
	j.mu.Unlock()
	s.sweepJobs()
	if _, ok := s.jobs["1"]; ok {
		t.Fatalf("到期后应被清理")
	}
}
