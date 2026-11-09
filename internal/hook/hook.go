// Package hook：触发器（事件钩子）。
//
// 设计：**判断在哪醒由外部决定，agent 只负责"被叫醒之后做什么"**。
// 定时、进程出现/消失、文件变化这些确定性条件交给 cron / inotifywait / 系统脚本，
// 它们调 `POST /api/trigger` 或 `agentd trigger <名字>`；agentd 只做：
//
//	查配额 → 查冷却 → 组装上下文 → 在一个固定会话里跑一轮
//
// 钩子定义放文件（可读、可 git、agent 自己能写）：~/.data/agentd/hooks/<名字>.json
//
//	{"name":"game-exit","mode":"speak","prompt":"我从游戏退出来了…",
//	 "quota_per_day":10,"cooldown_s":3}
//
// 防失控只靠配额：每次触发扣一次，用完当天不再响应。不做"深度"限制 ——
// 配额已经是硬上限，链条再长也会自己停。
package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 默认值
const (
	DefaultQuota    = 10              // 每个钩子每天最多触发几次
	DefaultCooldown = 3 * time.Second // 防脚本抖动（inotify 连环、脚本写错）
)

// Mode：speak = 让它说话；silent = 只做事、不主动开口
const (
	ModeSpeak  = "speak"
	ModeSilent = "silent"
)

// Def 是一个钩子的定义（只有"叫什么、说什么、限几次"，没有"什么时候"）
type Def struct {
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	Prompt      string `json:"prompt"`
	QuotaPerDay int    `json:"quota_per_day"`
	CooldownS   int    `json:"cooldown_s"`
}

func (d Def) quota() int {
	if d.QuotaPerDay > 0 {
		return d.QuotaPerDay
	}
	return DefaultQuota
}

func (d Def) cooldown() time.Duration {
	if d.CooldownS > 0 {
		return time.Duration(d.CooldownS) * time.Second
	}
	return DefaultCooldown
}

// Run 由调用方注入：真正"叫醒一轮 agent"的动作。
type Run func(ctx context.Context, d Def, text string) error

// Manager 管所有钩子：加载、配额、冷却、分发。
type Manager struct {
	Dir string
	Run Run

	mu         sync.Mutex
	defs       map[string]Def
	counts     map[string]int
	day        string
	lastAt     map[string]time.Time
	onErr      func(hook string, err error)
	onReloadFn func(names []string)
	fp         string // 上次 Load 时的目录指纹
}

func New(dir string, run Run) *Manager {
	return &Manager{
		Dir:    dir,
		Run:    run,
		defs:   map[string]Def{},
		counts: map[string]int{},
		day:    today(),
		lastAt: map[string]time.Time{},
	}
}

func today() string { return time.Now().Format("2006-01-02") }

// Load 读目录下所有 *.json。坏文件跳过并返回警告（不能因为一个坏钩子让整个功能失效）。
func (m *Manager) Load() []string {
	defs := map[string]Def{}
	var warns []string
	des, err := os.ReadDir(m.Dir)
	if err != nil {
		if !os.IsNotExist(err) {
			warns = append(warns, fmt.Sprintf("%s: %v", m.Dir, err))
		}
		m.mu.Lock()
		m.defs = defs
		m.mu.Unlock()
		return warns
	}
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(m.Dir, de.Name()))
		if err != nil {
			warns = append(warns, de.Name()+": "+err.Error())
			continue
		}
		var d Def
		if err := json.Unmarshal(b, &d); err != nil {
			warns = append(warns, de.Name()+": "+err.Error())
			continue
		}
		if strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Prompt) == "" {
			warns = append(warns, de.Name()+": 缺 name 或 prompt")
			continue
		}
		if d.Mode == "" {
			d.Mode = ModeSpeak
		}
		defs[d.Name] = d
	}
	m.mu.Lock()
	m.defs = defs
	m.fp = m.fingerprint()
	m.mu.Unlock()
	return warns
}

// fingerprint 是目录的"内容指纹"：文件名 + mtime + size。
// 只 stat 目录不够 —— 改已有文件的内容时目录 mtime 不变。
func (m *Manager) fingerprint() string {
	des, err := os.ReadDir(m.Dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d;", de.Name(), info.ModTime().UnixNano(), info.Size())
	}
	return b.String()
}

// WatchDir 轮询目录，变了就重读（改钩子不用重启 agentd）。
//
// 这里刻意用轮询而不是 fsnotify：钩子目录就那么几个文件，5 秒 stat 一次
// 的成本可以忽略，而 agentd 现在只依赖 sqlite 一个外部包，不值得为这事加一个。
func (m *Manager) WatchDir(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			last := m.fp
			m.mu.Unlock()
			if fp := m.fingerprint(); fp != last {
				if warns := m.Load(); len(warns) > 0 {
					for _, w := range warns {
						m.reportErr("load", fmt.Errorf("%s", w))
					}
				}
				m.onReload()
			}
		}
	}
}

// OnReload 重读后的回调（由调用方打日志：装了几个）
func (m *Manager) OnReload(f func(names []string)) { m.onReloadFn = f }

func (m *Manager) onReload() {
	m.mu.Lock()
	f := m.onReloadFn
	names := make([]string, 0, len(m.defs))
	for k := range m.defs {
		names = append(names, k)
	}
	m.mu.Unlock()
	sort.Strings(names)
	if f != nil {
		f(names)
	}
}

// Names 当前注册的钩子（排序，给 help/调试用）
func (m *Manager) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.defs))
	for k := range m.defs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// rollover 跨天清零配额
func (m *Manager) rollover() {
	if d := today(); d != m.day {
		m.day = d
		m.counts = map[string]int{}
	}
}

// Has 有没有注册这个钩子（内置事件源用：没注册就安静跳过，别报错刷日志）
func (m *Manager) Has(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.defs[name]
	return ok
}

// Trigger 触发一次。返回给人看的一句话（也会被写进模型上下文）。
//
// 三道检查，顺序不能换：存在性 → 冷却 → 配额。先查冷却是因为抖动会连续打进来，
// 每次扣配额会把额度浪费在重复事件上。
func (m *Manager) Trigger(ctx context.Context, name string, data map[string]any) (string, error) {
	m.mu.Lock()
	d, ok := m.defs[name]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("没有这个钩子：%s（可用：%s）", name, strings.Join(m.Names(), ", "))
	}
	m.rollover()
	if last, seen := m.lastAt[name]; seen && time.Since(last) < d.cooldown() {
		m.mu.Unlock()
		return "", fmt.Errorf("太快了：%s 刚触发过（冷却 %s）", name, d.cooldown())
	}
	if m.counts[name] >= d.quota() {
		m.mu.Unlock()
		return "", fmt.Errorf("今天 %s 已经触发满 %d 次了", name, d.quota())
	}
	m.counts[name]++
	m.lastAt[name] = time.Now()
	m.mu.Unlock()

	text := BuildText(d, data)
	used, quota := m.counts[name], d.quota()
	// 异步叫醒：触发接口必须立刻返回（脚本里 curl 不该被模型跑几分钟卡住），
	// 冷却检查在上面已经完成，所以抖动的第二次请求还是会在这里之前被拒。
	go func() {
		if err := m.Run(context.Background(), d, text); err != nil {
			m.reportErr(name, err)
		}
	}()
	return fmt.Sprintf("已触发 %s（今天第 %d/%d 次）", name, used, quota), nil
}

// BuildText 组装给模型的那段话：**必须带触发器和数据**，
// 否则模型不知道为什么被叫醒，会像无缘无故被吵醒一样乱猜。
func BuildText(d Def, data map[string]any) string {
	var b strings.Builder
	b.WriteString("[触发器 " + d.Name + "]")
	if len(data) > 0 {
		if raw, err := json.Marshal(data); err == nil {
			b.WriteString(" 数据：" + string(raw))
		}
	}
	b.WriteString("\n\n" + d.Prompt)
	if d.Mode == ModeSilent {
		b.WriteString("\n\n（这是静默触发：做该做的事，不要主动输出消息。）")
	}
	return b.String()
}

// OnError 触发执行失败时的回调（由调用方接日志）
func (m *Manager) OnError(f func(hook string, err error)) { m.onErr = f }

func (m *Manager) reportErr(name string, err error) {
	m.mu.Lock()
	f := m.onErr
	m.mu.Unlock()
	if f != nil {
		f(name, err)
	}
}

// Used 某钩子今天已用次数（测试/调试用）
func (m *Manager) Used(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollover()
	return m.counts[name]
}
