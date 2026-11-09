// Package logx：极简日志系统
// - 输出到 <数据目录>/xaihi.log（同时打到 stderr，systemd 里也能看）
// - 按大小滚动（默认 5MB × 3 份）
// - 行格式：2026-10-25T22:00:00+08:00 [INFO] 事件 key=value ...
package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxSize  = 5 << 20 // 5MB
	maxFiles = 3
)

var (
	mu   sync.Mutex
	f    *os.File
	path string
)

// Init 打开（或创建）日志文件；dir 为空则只输出到 stderr
func Init(dir string) error {
	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path = filepath.Join(dir, "xaihi.log")
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	f = fh
	return nil
}

func rotate() {
	if f == nil {
		return
	}
	st, err := f.Stat()
	if err != nil || st.Size() < maxSize {
		return
	}
	f.Close()
	for i := maxFiles - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", path, i)
		nw := fmt.Sprintf("%s.%d", path, i+1)
		os.Rename(old, nw) // 忽略错误（文件可能不存在）
	}
	os.Rename(path, path+".1")
	if fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		f = fh
	}
}

func write(level, msg string) {
	mu.Lock()
	defer mu.Unlock()
	line := time.Now().Format(time.RFC3339) + " [" + level + "] " + msg + "\n"
	os.Stderr.WriteString(line)
	if f != nil {
		f.WriteString(line)
		rotate()
	}
}

// 便捷函数
func Info(format string, a ...any)  { write("INFO", fmt.Sprintf(format, a...)) }
func Warn(format string, a ...any)  { write("WARN", fmt.Sprintf(format, a...)) }
func Error(format string, a ...any) { write("ERROR", fmt.Sprintf(format, a...)) }
