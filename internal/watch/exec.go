package watch

import "os"

// execMkdirAll 单独拆出来，免得 watch.go 里混进无关的 import
func execMkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }
