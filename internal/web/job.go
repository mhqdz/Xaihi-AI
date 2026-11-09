package web

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// job 保存一轮生成的 SSE 帧，供前端断点续传（刷新后逐字接上）。
type job struct {
	mu       sync.Mutex
	buf      []string
	done     bool
	expireAt time.Time // 结束后保留缓冲的时间；零值=不释放
}

func (s *Server) job(convID string) *job {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.jobs == nil {
		s.jobs = map[string]*job{}
	}
	j, ok := s.jobs[convID]
	if !ok {
		j = &job{}
		s.jobs[convID] = j
	}
	return j
}

func (j *job) reset() { j.mu.Lock(); j.buf = nil; j.done = false; j.mu.Unlock() }

func (j *job) emit(f string) {
	j.mu.Lock()
	j.buf = append(j.buf, f)
	j.expireAt = time.Time{} // 有新帧 → 暂停倒计时 ✓
	j.mu.Unlock()
}

func (j *job) finish() {
	j.mu.Lock()
	j.done = true
	j.expireAt = time.Now().Add(jobTTL()) // 完成后开始倒计时 ✓
	j.mu.Unlock()
}

func (j *job) snapshot(from int) ([]string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if from < 0 || from > len(j.buf) {
		from = len(j.buf)
	}
	out := make([]string, len(j.buf)-from)
	copy(out, j.buf[from:])
	return out, j.done
}

func (j *job) length() int { j.mu.Lock(); defer j.mu.Unlock(); return len(j.buf) }

// GET /api/conv/<id>/stream?from=N：先回放缓冲，再跟随后续帧；结束后返回。
//
// 这里所有刷行都用 http.NewResponseController(w).Flush() 而不是 w.(http.Flusher).Flush()：
// 客户端断开后底层 bufio 会拿到 ErrShortWrite 并 panic（见 web.go 里 send 的注释），
// 而这条路径在用户关标签页 / 刷新时同样会跑到。
func (s *Server) streamFrom(w http.ResponseWriter, r *http.Request, convID string) {
	if _, ok := w.(http.Flusher); !ok {
		writeJSON(w, 500, map[string]string{"error": "不支持流式"})
		return
	}
	from := 0
	if v := r.URL.Query().Get("from"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			from = n
		}
	}
	s.convMu.Lock()
	running := s.convCancel[convID] != nil
	s.convMu.Unlock()
	j := s.job(convID)
	if !running {
		// 没在跑是正常情况（比如打开一个已结束的会话）：给一个空的 done 流，不报错。
		// 历史消息走 /api/conv；这里不回放旧帧，避免前端误判成"正在运行"。
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		if _, err := fmt.Fprint(w, "event: done\ndata: {}\n\n"); err != nil {
			return
		}
		_ = http.NewResponseController(w).Flush()
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	idx := from
	lastBeat := time.Now()
	for {
		frames, done := j.snapshot(idx)
		for _, f := range frames {
			if _, err := w.Write([]byte(f)); err != nil {
				return
			}
			_ = http.NewResponseController(w).Flush()
			idx++
		}
		if done && len(frames) == 0 {
			if _, err := fmt.Fprint(w, "event: done\ndata: {}\n\n"); err != nil {
				return
			}
			_ = http.NewResponseController(w).Flush()
			return
		}
		if time.Since(lastBeat) > 5*time.Second {
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			_ = http.NewResponseController(w).Flush()
			lastBeat = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// jobTTL：完成后缓冲保留多久（默认 5 分钟，XAIHI_JOB_TTL 可覆盖，支持 30s/2m）
func jobTTL() time.Duration {
	if v := os.Getenv("XAIHI_JOB_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 5 * time.Minute
}

// sweepJobs：释放到期缓冲；运行中的轮次永不释放。
func (s *Server) sweepJobs() {
	now := time.Now()
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	for id, j := range s.jobs {
		s.convMu.Lock()
		running := s.convCancel[id] != nil
		s.convMu.Unlock()
		if running {
			continue
		}
		j.mu.Lock()
		exp := !j.expireAt.IsZero() && now.After(j.expireAt)
		j.mu.Unlock()
		if exp {
			delete(s.jobs, id)
		}
	}
}

// RunJobSweeper：每 20 秒清一次，随进程生命周期。
func (s *Server) RunJobSweeper() {
	go func() {
		for {
			time.Sleep(20 * time.Second)
			s.sweepJobs()
		}
	}()
}
