// Package wiki：人格自己维护的派生索引。wiki 不是事实源头，可以随时整理或重建。
// 每个人格 profile 一个独立根目录；kb 只能通过 id 链接，不能由本包写入。
package wiki

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"kbapp/core/entry"
)

const (
	FName    = "name"
	FUpdated = "updated"
	FKB      = "kb"

	MaxBodyRunes = 20000
)

var (
	ErrRevisionConflict = errors.New("wiki 页面版本冲突")
	ErrNotFound         = errors.New("wiki 页面不存在")
	reName              = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	reKBID              = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

type Entry struct {
	Name     string
	Body     string
	Updated  string
	KBLinks  []string
	Revision string
	fields   map[string]string
}

func DefaultRoot(dataDir string) string { return filepath.Join(dataDir, "wiki") }
func OwnerRoot(dataDir, owner string) string {
	return filepath.Join(DefaultRoot(dataDir), "personas", owner)
}

type Store struct{ Root string }

func New(root string) *Store { return &Store{Root: root} }

var rootLocks sync.Map

func rootMutex(root string) *sync.Mutex {
	key, err := filepath.Abs(root)
	if err != nil {
		key = filepath.Clean(root)
	}
	v, _ := rootLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

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

func revisionOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !reName.MatchString(name) {
		return "", fmt.Errorf("wiki 页面名只能用 a-z0-9_-（最多 64 字）：%q", name)
	}
	return name, nil
}

func inline(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func normalizeKBLinks(links []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, link := range links {
		for _, id := range strings.Split(link, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if !reKBID.MatchString(id) {
				return nil, fmt.Errorf("非法 kb id：%q", id)
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func format(fields map[string]string, body string) string {
	preferred := []string{FName, FUpdated, FKB}
	seen := map[string]bool{}
	var b strings.Builder
	b.WriteString("---\n")
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

func parseEntry(name string, raw []byte) Entry {
	fields, body := entry.ParseFM(string(raw))
	links, _ := normalizeKBLinks(strings.Split(fields[FKB], ","))
	return Entry{
		Name: name, Body: strings.TrimSpace(body), Updated: fields[FUpdated], KBLinks: links,
		Revision: revisionOf(raw), fields: fields,
	}
}

func (s *Store) path(name string) string { return filepath.Join(s.Root, name+".md") }

func (s *Store) readUnlocked(name string) (Entry, error) {
	name, err := validName(name)
	if err != nil {
		return Entry{}, err
	}
	raw, err := os.ReadFile(s.path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, fmt.Errorf("%w：%s", ErrNotFound, name)
		}
		return Entry{}, err
	}
	return parseEntry(name, raw), nil
}

func (s *Store) Read(name string) (out Entry, err error) {
	err = s.withLock(false, func() error {
		var err error
		out, err = s.readUnlocked(name)
		return err
	})
	return out, err
}

func (s *Store) List() (out []Entry, err error) {
	err = s.withLock(false, func() error {
		des, err := os.ReadDir(s.Root)
		if err != nil {
			return err
		}
		for _, de := range des {
			if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") || strings.HasPrefix(de.Name(), ".") {
				continue
			}
			name := strings.TrimSuffix(de.Name(), ".md")
			e, err := s.readUnlocked(name)
			if err != nil {
				continue
			}
			out = append(out, e)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return nil
	})
	return out, err
}

// Search 只在当前 Store（单一人格）内做简单子串检索，不接受任何跨域参数。
func (s *Store) Search(query string, limit int) (out []Entry, err error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, fmt.Errorf("query 不能为空")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, e := range all {
		haystack := strings.ToLower(e.Name + "\n" + e.Body + "\n" + strings.Join(e.KBLinks, ","))
		if strings.Contains(haystack, query) {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func stageFile(path string, data []byte) (tmp string, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".wiki-*.tmp")
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

func writeAtomic(path string, data []byte) error {
	tmp, err := stageFile(path, data)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, path)
}

func writeNewAtomic(path string, data []byte) error {
	tmp, err := stageFile(path, data)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Link(tmp, path)
}

// Write 新建或更新页面。新建时 expectedRevision 必须为空；更新时必须带最近读取的 revision。
func (s *Store) Write(name, body string, kbLinks []string, expectedRevision string) (out Entry, err error) {
	name, err = validName(name)
	if err != nil {
		return Entry{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Entry{}, fmt.Errorf("wiki 正文不能为空")
	}
	if len([]rune(body)) > MaxBodyRunes {
		return Entry{}, fmt.Errorf("wiki 单页最多 %d 字", MaxBodyRunes)
	}
	kbLinks, err = normalizeKBLinks(kbLinks)
	if err != nil {
		return Entry{}, err
	}
	err = s.withLock(true, func() error {
		old, readErr := s.readUnlocked(name)
		exists := readErr == nil
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return readErr
		}
		if exists {
			if expectedRevision == "" {
				return fmt.Errorf("页面 %s 已存在，更新必须携带 revision", name)
			}
			if old.Revision != expectedRevision {
				return fmt.Errorf("%w：%s 已被修改，请重新读取", ErrRevisionConflict, name)
			}
		} else if expectedRevision != "" {
			return fmt.Errorf("页面 %s 不存在，不能按旧 revision 更新", name)
		}
		fields := map[string]string{}
		if exists {
			for k, v := range old.fields {
				fields[k] = v
			}
		}
		fields[FName] = name
		fields[FUpdated] = time.Now().Format("2006-01-02")
		fields[FKB] = strings.Join(kbLinks, ",")
		raw := []byte(format(fields, body))
		path := s.path(name)
		if exists {
			err = writeAtomic(path, raw)
		} else {
			err = writeNewAtomic(path, raw)
		}
		if err != nil {
			return err
		}
		out = parseEntry(name, raw)
		return nil
	})
	return out, err
}

// Delete 删除可重建的派生页面；revision 防止误删别人刚更新的版本。
func (s *Store) Delete(name, expectedRevision string) error {
	if expectedRevision == "" {
		return fmt.Errorf("删除 wiki 页面必须携带 revision")
	}
	return s.withLock(true, func() error {
		e, err := s.readUnlocked(name)
		if err != nil {
			return err
		}
		if e.Revision != expectedRevision {
			return fmt.Errorf("%w：%s 已被修改，请重新读取", ErrRevisionConflict, name)
		}
		return os.Remove(s.path(e.Name))
	})
}
