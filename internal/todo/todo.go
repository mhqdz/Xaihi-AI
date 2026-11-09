// Package todo：待办。
//
// 定位：**给她一个"该提醒你什么"的清单**，不是任务管理系统。
// 只做三件事：存、列、标记完成。到点提醒不在这里做 —— 由外部脚本读这份文件
// （或者在你自己的云端待办里）去驱动 `xaihi trigger`。
//
// 为什么单独一层而不是塞进记忆：记忆是"关于你的长期事实"，待办是"有到期时间
// 的一次性约定"。混在一起会让记忆预算被过期事项占满。
package todo

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxItems：上限。到顶了拒绝新增，而不是悄悄挤掉最旧的 ——
// 待办被静默丢弃比报错难受得多。
const MaxItems = 50

// Item 一条待办
type Item struct {
	ID     string    `json:"id"`
	What   string    `json:"what"`
	At     time.Time `json:"at"` // 到期时间（空 = 不指定，随时可提）
	Done   bool      `json:"done"`
	SaidAt time.Time `json:"said_at,omitempty"` // 提醒过的时间（外部脚本用）
	ConvID int64     `json:"conv_id,omitempty"` // 在哪个会话交代的
}

// Store 一个 json 文件（读-改-写，量小）
type Store struct{ Path string }

func Open(path string) *Store { return &Store{Path: path} }

func (s *Store) load() ([]Item, error) {
	b, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []Item
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, fmt.Errorf("待办文件坏了（%s）：%w", s.Path, err)
	}
	return items, nil
}

func (s *Store) save(items []Item) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// List 全部（含已完成的，按到期时间升序；没时间的排最后）
func (s *Store) List() ([]Item, error) {
	items, err := s.load()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
	return items, nil
}

func less(a, b Item) bool {
	if a.Done != b.Done {
		return !a.Done // 未完成在前
	}
	if a.At.IsZero() != b.At.IsZero() {
		return !a.At.IsZero() // 有时间的在前
	}
	return a.At.Before(b.At)
}

// Add 新增一条。what 去掉首尾空白；空则报错。
func (s *Store) Add(what string, at time.Time, convID int64) (Item, error) {
	what = strings.TrimSpace(what)
	if what == "" {
		return Item{}, fmt.Errorf("待办内容不能为空")
	}
	items, err := s.load()
	if err != nil {
		return Item{}, err
	}
	if len(items) >= MaxItems {
		return Item{}, fmt.Errorf("待办已经有 %d 条了（上限 %d），先完成或删掉一些", len(items), MaxItems)
	}
	id := at.UTC().Format("20060102") + "-" + randHex(3)
	it := Item{ID: id, What: what, At: at, ConvID: convID}
	items = append(items, it)
	return it, s.save(items)
}

// Done 标记完成（保留记录，不删 —— 和记忆的软删一个思路）
func (s *Store) Done(id string) error {
	items, err := s.load()
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].ID == id {
			items[i].Done = true
			return s.save(items)
		}
	}
	return fmt.Errorf("没有这条待办：%s", id)
}

// Remove 真删（误建的用）
func (s *Store) Remove(id string) error {
	items, err := s.load()
	if err != nil {
		return err
	}
	out := items[:0]
	found := false
	for _, it := range items {
		if it.ID == id {
			found = true
			continue
		}
		out = append(out, it)
	}
	if !found {
		return fmt.Errorf("没有这条待办：%s", id)
	}
	return s.save(out)
}

// Pending 未完成的，最多 limit 条（0 = 全部）
func (s *Store) Pending(limit int) ([]Item, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(all))
	for _, it := range all {
		if it.Done {
			continue
		}
		out = append(out, it)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// MarkSaid 记一次"已经提醒过"（外部脚本触发后调它，避免重复催）
func (s *Store) MarkSaid(id string, at time.Time) error {
	items, err := s.load()
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].ID == id {
			items[i].SaidAt = at
			return s.save(items)
		}
	}
	return fmt.Errorf("没有这条待办：%s", id)
}

// Render 注入用的那几行（未完成、最多 limit 条）。
// 超过 limit 的只报个数 —— 别让清单吃掉上下文。
func (s *Store) Render(now time.Time, limit int) string {
	items, err := s.Pending(limit)
	if err != nil || len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[待办]")
	for _, it := range items {
		b.WriteString("\n- " + it.ID + "  " + it.What)
		if !it.At.IsZero() {
			if it.At.Before(now) {
				b.WriteString("（原定 " + it.At.Format("01-02 15:04") + "，已过）")
			} else {
				b.WriteString("（" + it.At.Format("01-02 15:04") + "）")
			}
		}
	}
	if all, _ := s.Pending(0); len(all) > len(items) {
		b.WriteString(fmt.Sprintf("\n（还有 %d 条没列出来）", len(all)-len(items)))
	}
	return b.String()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "000000"
	}
	return hex.EncodeToString(b)
}
