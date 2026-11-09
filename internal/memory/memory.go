// Package memory：长期记忆（人可读、可编辑的 .md 文件，每次拼进 system prompt）。
//
// 和 kb 的分工：kb 是"外部知识，用时才查"；记忆是"内在状态，一直在场"。
// 判据：这条东西下次说话时需不需要它一直在场？需要 → 记忆；只是可能有用 → kb。
//
// 存储（撞车率最低的形态：目录 + front matter，人能直接改、能 git）：
//
//	~/.data/agentd/memory/
//	  global/<id>.md            跨项目的（偏好、关系、经历）
//	  project/<name>/<id>.md    只跟这个项目有关的
//
// front matter 由本包生成：id / created / scope / kind / status / source。
// status 只有 active / superseded / archived 三种：后两者仍在磁盘上（可查、可恢复），
// 但不进提示词。删除是显式动作（Purge），不做自动衰减 —— 量小的时候人清理更快。
package memory

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kbapp/core/entry"
)

// 一条记忆的元数据字段
const (
	FID      = "id"
	FCreated = "created"
	FScope   = "scope"
	FKind    = "kind"
	FStatus  = "status"
	FSource  = "source"

	// StatusActive 才会进提示词
	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusArchived   = "archived"
)

// 正文上限：一条记忆不是一篇文章，超过这个就不是"一句话能记的事"
const MaxBodyRunes = 2000



// Entry 是一条记忆。Name 是文件名（不含 .md），也用于 forget。
type Entry struct {
	Name   string
	Fields map[string]string
	Body   string
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

// DefaultRoot：~/.data/agentd/memory
func DefaultRoot(dataDir string) string { return filepath.Join(dataDir, "memory") }

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

// ---------- 解析与序列化 ----------

// parse 复用 kb 的 core/entry：两边是同一套 front matter 格式，
// 以后改格式只改一处（这也是抽 core 的意义）。
func parse(text string) (map[string]string, string) {
	return entry.ParseFM(text)
}

func format(fields map[string]string, body string) string {
	keys := []string{FID, FCreated, FScope, FKind, FStatus, FSource}
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range keys {
		if v := fields[k]; v != "" {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
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
		out = append(out, Entry{Name: strings.TrimSuffix(n, ".md"), Fields: f, Body: strings.TrimSpace(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// List 返回 active 的记忆（全局 + 当前项目），按 created 升序。
func (s *Store) List() ([]Entry, error) {
	var all []Entry
	for _, sc := range s.scopes() {
		es, err := s.readDir(sc)
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			if e.Status() == StatusActive && strings.TrimSpace(e.Body) != "" {
				all = append(all, e)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Created() < all[j].Created() })
	return all, nil
}

// ---------- 写入 ----------

func newID() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// Save 写一条记忆。scope 为空时用 Store.Scope（没有就 global）。
// 返回文件名，后面忘记/替换都用它。
func (s *Store) Save(scope, kind, source, body string) (Entry, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Entry{}, fmt.Errorf("正文是空的")
	}
	if n := len([]rune(body)); n > MaxBodyRunes {
		return Entry{}, fmt.Errorf("太长了：%d 字，一条记忆最多 %d 字（拆成几条更清楚）", n, MaxBodyRunes)
	}
	if scope == "" {
		scope = s.Scope
	}
	if scope == "" {
		scope = "global"
	}
	id := newID()
	fields := map[string]string{
		FID:      id,
		FCreated: time.Now().Format("2006-01-02"),
		FScope:   scope,
		FKind:    kind,
		FStatus:  StatusActive,
		FSource:  source,
	}
	d := s.dir(scope)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return Entry{}, err
	}
	name := id
	if err := os.WriteFile(filepath.Join(d, name+".md"), []byte(format(fields, body)), 0o644); err != nil {
		return Entry{}, err
	}
	return Entry{Name: name, Fields: fields, Body: body}, nil
}

// find 在两层里找一条记忆（名字唯一即可，不用带 scope）。
func (s *Store) find(name string) (string, map[string]string, error) {
	name = strings.TrimSuffix(name, ".md")
	for _, sc := range []string{"global", s.Scope} {
		if sc == "" {
			continue
		}
		p := filepath.Join(s.dir(sc), name+".md")
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		f, _ := parse(string(b))
		return sc, f, nil
	}
	return "", nil, fmt.Errorf("没有这条记忆：%s", name)
}

// Forget 软删除：标 archived（不进提示词，但文件还在，可以翻回来）。
func (s *Store) Forget(name string) error { return s.setStatus(name, StatusArchived) }

// Supersede 标"已被替代"：内容还对、但已经不是当前说法（保留下来可回溯）。
func (s *Store) Supersede(name string) error { return s.setStatus(name, StatusSuperseded) }

// Restore 恢复成 active。
func (s *Store) Restore(name string) error { return s.setStatus(name, StatusActive) }

// Purge 真删文件（人工确认后用，不进模型工具）。
func (s *Store) Purge(name string) error {
	sc, _, err := s.find(name)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(s.dir(sc), strings.TrimSuffix(name, ".md")+".md"))
}

func (s *Store) setStatus(name, status string) error {
	sc, f, err := s.find(name)
	if err != nil {
		return err
	}
	name = strings.TrimSuffix(name, ".md")
	p := filepath.Join(s.dir(sc), name+".md")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	f[FStatus] = status
	_, body := parse(string(b))
	return os.WriteFile(p, []byte(format(f, body)), 0o644)
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

// Render 拼出要注入 system prompt 的段落。
//
// 挑选与拼装顺序刻意分开：按 created **从新到旧**挑（近期写的才是"她记得的事"），
// 拼装时再**排回升序** —— 同样一组记忆必须产出同样的前缀，否则每次请求都不一样，
// 上游的 KV 缓存会全部作废。
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
		b.WriteString("\n- " + kind + "（" + e.Created() + "）：" + strings.TrimSpace(e.Body) + "\n")
	}
	return b.String()
}
