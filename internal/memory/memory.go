// Package memory：长期记忆（人可读、可编辑的 .md 文件，每次拼进 system prompt）。
//
// 和 kb / wiki 的分工：
//   - memory 是 agent 自己的笔记，会腐坏，需要凭 transcripts 校对和修订；
//   - wiki 是 agent 自己维护的派生索引，可以重建；
//   - kb 是人审过的外部事实，agent 只读，可通过 id 链接。
//
// 存储（撞车率最低的形态：目录 + front matter，人能直接改、能 git）：
//
//	~/.data/xaihi/memory/
//	  global/<id>.md            跨项目的（偏好、关系、经历）
//	  project/<name>/<id>.md    只跟这个项目有关的
//
// front matter 由本包生成：id / created / updated / scope / kind / status /
// source / basis / superseded_by / supersedes。
// status 只有 active / superseded / archived 三种：后两者仍在磁盘上（可查、可恢复），
// 但不进提示词。删除是显式动作（Purge），不做自动衰减。
//
// source 尽量指向触发写入的 conv:<id>#<seq>；basis 区分 explicit（用户明确说）、
// verified（声称有工具或转录证据）和 inferred（模型推断），不伪造数值置信度。
// 两者都是校对线索，不自动证明正文为真；dreaming 仍须回查原始证据。
package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"kbapp/core/entry"
)

// 一条记忆的元数据字段
const (
	FID           = "id"
	FCreated      = "created"
	FUpdated      = "updated"
	FScope        = "scope"
	FKind         = "kind"
	FStatus       = "status"
	FSource       = "source"
	FBasis        = "basis"
	FSupersededBy = "superseded_by"
	FSupersedes   = "supersedes"

	// StatusActive 才会进提示词
	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusArchived   = "archived"

	// 记忆依据：只表达证据类型，不表达模型主观置信度。
	BasisExplicit = "explicit"
	BasisVerified = "verified"
	BasisInferred = "inferred"
)

// ErrRevisionConflict 表示读取后被别处修改；调用方必须重读，不能覆盖新版本。
var ErrRevisionConflict = errors.New("记忆版本冲突")

// 正文上限：一条记忆不是一篇文章，超过这个就不是"一句话能记的事"
const MaxBodyRunes = 2000

// Entry 是一条记忆。Name 是文件名（不含 .md），也用于 forget。
// Revision 是原始文件字节的 SHA-256，供替换时做乐观并发检查。
type Entry struct {
	Name     string
	Fields   map[string]string
	Body     string
	Revision string
}

func (e Entry) Field(k string) string { return e.Fields[k] }
func (e Entry) Status() string {
	if s := e.Fields[FStatus]; s != "" {
		return s
	}
	return StatusActive
}
func (e Entry) Created() string { return e.Fields[FCreated] }

// Store 指向记忆根目录。Scope 是"当前项目"的名字（空 = 只有全局）。
type Store struct {
	Root  string
	Scope string
}

func New(root string) *Store { return &Store{Root: root} }

// DefaultRoot：~/.data/xaihi/memory（旧共享结构及新结构的共同根）。
func DefaultRoot(dataDir string) string { return filepath.Join(dataDir, "memory") }

// OwnerRoot：某一人格 profile 的独立记忆根。global/project 是保留的旧目录名，
// 因此新增 personas 一层，避免人格叫 global 时与旧数据混在一起。
func OwnerRoot(dataDir, owner string) string {
	return filepath.Join(DefaultRoot(dataDir), "personas", owner)
}

// MigrateLegacy 把旧共享 global/project 记忆复制到唯一明确的主人名下。
// 只复制、不删除旧数据；已存在的同名文件绝不覆盖。多个人格时调用方不得猜测主人。
func MigrateLegacy(root, legacy string) (int, error) {
	if filepath.Clean(root) == filepath.Clean(legacy) {
		return 0, fmt.Errorf("新记忆根不能就是旧共享根")
	}
	marker := filepath.Join(root, ".legacy-migrated")
	if _, err := os.Stat(marker); err == nil {
		return 0, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 0, err
	}
	copied := 0
	for _, top := range []string{"global", "project"} {
		srcRoot := filepath.Join(legacy, top)
		err := filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") || !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(legacy, path)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			target := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeNewAtomic(target, raw, 0o644); err != nil {
				if os.IsExist(err) {
					return nil
				}
				return err
			}
			copied++
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return copied, err
		}
	}
	mark := []byte("旧共享记忆已复制；原目录保留作备份。源：" + legacy + "\n")
	if err := writeNewAtomic(marker, mark, 0o644); err != nil && !os.IsExist(err) {
		return copied, err
	}
	return copied, nil
}

func (s *Store) dir(scope string) string {
	if scope == "" || scope == "global" {
		return filepath.Join(s.Root, "global")
	}
	return filepath.Join(s.Root, "project", scope)
}

// scopes 返回"当前该注入哪几层"：全局永远有，项目层有名字才加。
func (s *Store) scopes() []string {
	if s.Scope == "" {
		return []string{"global"}
	}
	return []string{"global", s.Scope}
}

// 同一进程里不同 Agent 会各自创建 Store；按根目录共享互斥锁，避免锁只保护单个实例。
var rootLocks sync.Map // map[absolute root]*sync.Mutex

func rootMutex(root string) *sync.Mutex {
	key, err := filepath.Abs(root)
	if err != nil {
		key = filepath.Clean(root)
	}
	v, _ := rootLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// withLock 同时保护同进程的不同 Store 和不同 xaihi 进程。
// 人工编辑器不参与 flock，所以 dreaming/替换仍必须携带 Revision 做乐观检查。
func (s *Store) withLock(exclusive bool, fn func() error) error {
	mu := rootMutex(s.Root)
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Root, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	op := syscall.LOCK_SH
	if exclusive {
		op = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), op); err != nil {
		_ = f.Close()
		return err
	}
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}()
	return fn()
}

// ---------- 原子文件写 ----------

// stageFile 先在目标目录写完并 fsync 一个临时文件，之后 link/rename 都是原子操作。
func stageFile(path string, data []byte, perm os.FileMode) (tmp string, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmp = f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(perm); err != nil {
		return "", err
	}
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	return tmp, nil
}

func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}

// writeAtomic 替换已有文件；读者只会看到完整旧版本或完整新版本。
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := stageFile(path, data, perm)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// writeNewAtomic 用硬链接实现“只创建、不覆盖”，避免撞 ID 时丢掉已有记忆。
func writeNewAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := stageFile(path, data, perm)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// ---------- 解析与序列化 ----------

// parse 复用 kb 的 core/entry：两边是同一套 front matter 格式。
func parse(text string) (map[string]string, string) {
	return entry.ParseFM(text)
}

func inline(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// format 保留人工添加或未来版本写入的未知 front-matter 字段，避免状态变更时丢元数据。
func format(fields map[string]string, body string) string {
	preferred := []string{FID, FCreated, FUpdated, FScope, FKind, FStatus, FSource, FBasis, FSupersededBy, FSupersedes}
	var b strings.Builder
	b.WriteString("---\n")
	seen := make(map[string]bool, len(preferred))
	for _, k := range preferred {
		seen[k] = true
		if v := inline(fields[k]); v != "" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	var extra []string
	for k := range fields {
		if k != "" && !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		b.WriteString(inline(k) + ": " + inline(fields[k]) + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

func revisionOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// readDir 读一个目录下的 .md（不含模板/隐藏文件），按文件名升序。
func (s *Store) readDir(scope string) ([]Entry, error) {
	d := s.dir(scope)
	des, err := os.ReadDir(d)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 还没写过记忆是正常状态
		}
		return nil, err
	}
	var out []Entry
	for _, de := range des {
		n := de.Name()
		if de.IsDir() || !strings.HasSuffix(n, ".md") || strings.HasPrefix(n, ".") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(d, n))
		if err != nil {
			continue
		}
		f, body := parse(string(raw))
		if f[FScope] == "" {
			f[FScope] = scope
		}
		out = append(out, Entry{
			Name: strings.TrimSuffix(n, ".md"), Fields: f, Body: strings.TrimSpace(body), Revision: revisionOf(raw),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// List 返回 active 的记忆（全局 + 当前项目），按 created 升序。
func (s *Store) List() (out []Entry, err error) {
	err = s.withLock(false, func() error {
		var all []Entry
		for _, sc := range s.scopes() {
			es, err := s.readDir(sc)
			if err != nil {
				return err
			}
			for _, e := range es {
				if e.Status() == StatusActive && strings.TrimSpace(e.Body) != "" {
					all = append(all, e)
				}
			}
		}
		sort.SliceStable(all, func(i, j int) bool { return all[i].Created() < all[j].Created() })
		out = all
		return nil
	})
	return out, err
}

// ---------- 写入 ----------

func newID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b), nil
}

func normalizeBasis(basis string) (string, error) {
	basis = strings.TrimSpace(basis)
	if basis == "" {
		return BasisInferred, nil
	}
	switch basis {
	case BasisExplicit, BasisVerified, BasisInferred:
		return basis, nil
	default:
		return "", fmt.Errorf("未知 basis %q（只能是 explicit / verified / inferred）", basis)
	}
}

// Save 写一条记忆。scope 为空时用 Store.Scope（没有就 global）。
// 兼容内部旧调用：未指定依据时明确记为 inferred，而不是当成已验证事实。
func (s *Store) Save(scope, kind, source, body string) (Entry, error) {
	return s.SaveWithBasis(scope, kind, source, "", body)
}

// SaveWithBasis 写一条带证据类型的记忆，返回文件名和 Revision。
func (s *Store) SaveWithBasis(scope, kind, source, basis, body string) (Entry, error) {
	basis, err := normalizeBasis(basis)
	if err != nil {
		return Entry{}, err
	}
	var out Entry
	err = s.withLock(true, func() error {
		var err error
		out, err = s.saveLocked(scope, kind, source, basis, body, nil)
		return err
	})
	return out, err
}

// saveLocked 的调用方必须持有写锁。extra 只由本包用于替换链等内部元数据。
func (s *Store) saveLocked(scope, kind, source, basis, body string, extra map[string]string) (Entry, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Entry{}, fmt.Errorf("正文是空的")
	}
	if n := len([]rune(body)); n > MaxBodyRunes {
		return Entry{}, fmt.Errorf("太长了：%d 字，一条记忆最多 %d 字（拆成几条更清楚）", n, MaxBodyRunes)
	}
	basis, err := normalizeBasis(basis)
	if err != nil {
		return Entry{}, err
	}
	if scope == "" {
		scope = s.Scope
	}
	if scope == "" {
		scope = "global"
	}
	d := s.dir(scope)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return Entry{}, err
	}
	for attempt := 0; attempt < 8; attempt++ {
		id, err := newID()
		if err != nil {
			return Entry{}, err
		}
		fields := map[string]string{
			FID:      id,
			FCreated: time.Now().Format("2006-01-02"),
			FScope:   scope,
			FKind:    kind,
			FStatus:  StatusActive,
			FSource:  source,
			FBasis:   basis,
		}
		for k, v := range extra {
			fields[k] = v
		}
		raw := []byte(format(fields, body))
		if err := writeNewAtomic(filepath.Join(d, id+".md"), raw, 0o644); err != nil {
			if os.IsExist(err) {
				continue
			}
			return Entry{}, err
		}
		return Entry{Name: id, Fields: fields, Body: body, Revision: revisionOf(raw)}, nil
	}
	return Entry{}, fmt.Errorf("连续生成重复的记忆 id，已放弃写入")
}

// ---------- 定位、状态与替换 ----------

type record struct {
	Scope    string
	Name     string
	Path     string
	Fields   map[string]string
	Body     string
	Revision string
}

func validName(name string) (string, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".md")
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.Contains(name, `\`) {
		return "", fmt.Errorf("非法记忆名字：%q", name)
	}
	return name, nil
}

// find 的调用方必须持有读锁或写锁。
func (s *Store) find(name string) (record, error) {
	name, err := validName(name)
	if err != nil {
		return record{}, err
	}
	for _, sc := range []string{"global", s.Scope} {
		if sc == "" {
			continue
		}
		p := filepath.Join(s.dir(sc), name+".md")
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		f, body := parse(string(raw))
		if f[FScope] == "" {
			f[FScope] = sc
		}
		return record{
			Scope: sc, Name: name, Path: p, Fields: f, Body: strings.TrimSpace(body), Revision: revisionOf(raw),
		}, nil
	}
	return record{}, fmt.Errorf("没有这条记忆：%s", name)
}

// Forget 软删除：标 archived（不进提示词，但文件还在，可以翻回来）。
func (s *Store) Forget(name string) error { return s.setStatus(name, StatusArchived) }

// Supersede 标"已被替代"。新代码应优先用 Replace，以同时建立新版本和双向链接。
func (s *Store) Supersede(name string) error { return s.setStatus(name, StatusSuperseded) }

// Restore 恢复成 active。
func (s *Store) Restore(name string) error { return s.setStatus(name, StatusActive) }

// Purge 真删文件（人工确认后用，不进模型工具）。
func (s *Store) Purge(name string) error {
	return s.withLock(true, func() error {
		r, err := s.find(name)
		if err != nil {
			return err
		}
		if err := os.Remove(r.Path); err != nil {
			return err
		}
		syncDir(filepath.Dir(r.Path))
		return nil
	})
}

func (s *Store) setStatus(name, status string) error {
	return s.withLock(true, func() error {
		r, err := s.find(name)
		if err != nil {
			return err
		}
		r.Fields[FStatus] = status
		r.Fields[FUpdated] = time.Now().Format("2006-01-02")
		return writeAtomic(r.Path, []byte(format(r.Fields, r.Body)), 0o644)
	})
}

// Replace 创建一条新记忆，并把旧记忆标为 superseded；旧正文不被覆盖。
// expectedRevision 必须来自调用方最近一次读取，防止 dreaming 或模型覆盖并发编辑。
func (s *Store) Replace(name, expectedRevision, kind, source, basis, body string) (Entry, error) {
	if strings.TrimSpace(expectedRevision) == "" {
		return Entry{}, fmt.Errorf("缺少 expectedRevision：替换前必须先读取记忆")
	}
	var out Entry
	err := s.withLock(true, func() error {
		old, err := s.find(name)
		if err != nil {
			return err
		}
		if old.Revision != expectedRevision {
			return fmt.Errorf("%w：%s 已被别处修改，请重新读取", ErrRevisionConflict, name)
		}
		status := old.Fields[FStatus]
		if status == "" {
			status = StatusActive
		}
		if status != StatusActive {
			return fmt.Errorf("只能替换 active 记忆，%s 当前是 %s", name, status)
		}
		if strings.TrimSpace(kind) == "" {
			kind = old.Fields[FKind]
		}
		if strings.TrimSpace(basis) == "" {
			basis = old.Fields[FBasis]
		}
		fresh, err := s.saveLocked(old.Fields[FScope], kind, source, basis, body, map[string]string{FSupersedes: old.Name})
		if err != nil {
			return err
		}
		freshPath := filepath.Join(s.dir(fresh.Fields[FScope]), fresh.Name+".md")
		rollback := func(cause error) error {
			if err := os.Remove(freshPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("%w；回滚新记忆 %s 也失败：%v", cause, fresh.Name, err)
			}
			syncDir(filepath.Dir(freshPath))
			return cause
		}

		// flock 无法约束人工编辑器；写入旧文件前再验一次，尽量缩短并发编辑窗口。
		raw, err := os.ReadFile(old.Path)
		if err != nil {
			return rollback(err)
		}
		if revisionOf(raw) != expectedRevision {
			return rollback(fmt.Errorf("%w：%s 已被人工修改，请重新读取", ErrRevisionConflict, name))
		}
		fields, oldBody := parse(string(raw))
		fields[FStatus] = StatusSuperseded
		fields[FSupersededBy] = fresh.Name
		fields[FUpdated] = time.Now().Format("2006-01-02")
		if err := writeAtomic(old.Path, []byte(format(fields, oldBody)), 0o644); err != nil {
			return rollback(err)
		}
		out = fresh
		return nil
	})
	return out, err
}

// ---------- 注入 ----------

// 估算 token：CJK 每码点 1，ASCII 连续段 ÷4（中文为主，够准且便宜）
func estTokens(s string) int {
	n, ascii := 0, 0
	for _, r := range s {
		if r > 127 {
			n += (ascii+3)/4 + 1
			ascii = 0
		} else {
			ascii++
		}
	}
	return n + (ascii+3)/4
}

func basisLabel(basis string) string {
	switch basis {
	case BasisExplicit:
		return "用户明确"
	case BasisVerified:
		return "已验证"
	case BasisInferred:
		return "推断"
	default:
		return ""
	}
}

// Render 拼出要注入 system prompt 的段落。
//
// 挑选与拼装顺序刻意分开：按 created **从新到旧**挑（近期写的才是"她记得的事"），
// 拼装时再**排回升序** —— 同样一组记忆必须产出同样的前缀，否则每次请求都不一样，
// 上游的 KV 缓存会全部作废。
// BudgetFor 按模型窗口算记忆预算：窗口的 5%，封顶 6000 token（估算值）。
func BudgetFor(contextWindow int) int {
	if contextWindow <= 0 {
		return 6000
	}
	if b := contextWindow / 20; b < 6000 {
		return b
	}
	return 6000
}

func (s *Store) Render(budget int) string {
	if budget <= 0 {
		budget = 6000
	}
	all, err := s.List()
	if err != nil || len(all) == 0 {
		return ""
	}
	byNewest := make([]Entry, len(all))
	for i, e := range all {
		byNewest[len(all)-1-i] = e
	}
	chosen := map[string]bool{}
	used := 0
	for _, e := range byNewest {
		t := estTokens(e.Body) + 8 // 8 是标题/分隔的粗略开销
		if used+t > budget {
			continue // 装不下就跳过，继续看更短的
		}
		chosen[e.Name] = true
		used += t
	}
	if len(chosen) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[记忆] 以下是我记得的事（人工维护，可直接编辑文件）。\n")
	for _, e := range all {
		if !chosen[e.Name] {
			continue
		}
		kind := e.Field(FKind)
		if kind == "" {
			kind = "记录"
		}
		stamp := e.Created()
		if label := basisLabel(e.Field(FBasis)); label != "" {
			stamp += "，" + label
		}
		b.WriteString("\n- " + kind + "（" + stamp + "）：" + strings.TrimSpace(e.Body) + "\n")
	}
	return b.String()
}
