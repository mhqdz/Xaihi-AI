package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestRetryableNetErr(t *testing.T) {
	yes := []error{
		io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.EPIPE,
		fmt.Errorf("Post \"https://x\": EOF"),
		fmt.Errorf("read tcp: connection reset by peer"),
	}
	for _, e := range yes {
		if !retryableNetErr(e) {
			t.Errorf("应该可重试: %v", e)
		}
	}
	no := []error{nil, errors.New("invalid api key"), errors.New("context canceled")}
	for _, e := range no {
		if retryableNetErr(e) {
			t.Errorf("不该重试: %v", e)
		}
	}
}

// 5xx 会重试一次，之后成功就返回成功
func TestPostRetries5xx(t *testing.T) {
	var n int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"temporarily down"}`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer up.Close()
	c := New(up.URL, "")
	resp, err := c.post(context.Background(), map[string]string{"x": "y"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("应该重试后拿到 200，得到 %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&n) != 2 {
		t.Errorf("应该请求两次，实际 %d", n)
	}
}

// 4xx 不重试
func TestPostNoRetry4xx(t *testing.T) {
	var n int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"bad key"}`)
	}))
	defer up.Close()
	c := New(up.URL, "k")
	resp, err := c.post(context.Background(), map[string]string{"x": "y"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if atomic.LoadInt32(&n) != 1 {
		t.Errorf("4xx 不该重试，实际请求 %d 次", n)
	}
}
