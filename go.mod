module xaihi

go 1.26.0

require kbapp/core v0.0.0

require modernc.org/sqlite v1.61.0

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.49.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

// 本地路径：core 是 KnowledgeBase 仓库里的独立 module，记忆/知识两边共用一份
replace kbapp/core => ../KnowledgeBase/core
