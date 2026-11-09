// Package journal：叙事层 —— "她过过的日子"。
//
// 和记忆的分工：
//
//	memory  记事实（他喜欢什么、约定了什么）→ 注入，一直在场
//	journal 记经历（那天发生了什么、我怎么想）→ 不注入，按日期读
//
// 为什么一天一个文件：日记最容易被骂"无限增长，最后还是得截断"。
// 按天切之后，读取天然有界（"最近七天"就是七个文件），而且日期是最可靠的索引 ——
// 不需要检索，不会"搜不到"。
package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxRunes：一天最多写多少字。超了拒绝而不是截断 —— 截断会悄悄丢她写的东西。
const MaxRunes = 500

var weekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

type Store struct{ Dir string }

func (s *Store) dayName(day time.Time) string { return day.Format("2006-01-02") }

// Path 某天的文件路径
func (s *Store) Path(day time.Time) string { return filepath.Join(s.Dir, s.dayName(day)+".md") }

// Read 读某天（没有就是空串）
func (s *Store) Read(day time.Time) (string, error) {
	b, err := os.ReadFile(s.Path(day))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// Append 往某天追加一段（按时间分小节）。超过 MaxRunes 报错。
func (s *Store) Append(day, at time.Time, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("日记不能是空的")
	}
	if n := utf8.RuneCountInString(text); n > MaxRunes {
		return fmt.Errorf("这段 %d 字，超过一天 %d 字的上限（写短点，细节留给记忆和事件）", n, MaxRunes)
	}
	cur, err := s.Read(day)
	if err != nil {
		return err
	}
	if cur == "" {
		cur = fmt.Sprintf("# %s %s\n", s.dayName(day), weekdays[int(day.Weekday())])
	} else if n := utf8.RuneCountInString(cur); n > MaxRunes {
		return fmt.Errorf("今天的日记已经 %d 字，超过上限 %d 了", n, MaxRunes)
	}
	cur += fmt.Sprintf("\n## %s\n\n%s\n", at.Format("15:04"), text)
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.Path(day), []byte(cur), 0o644)
}

// Days 已有的日记（按日期升序）
func (s *Store) Days() ([]string, error) {
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(out)
	return out, nil
}

// Hint 注入 system prompt 的那一行：**只说路径，不塞内容**。
// 内容塞进去就会吃掉记忆的预算，而且日记会越来越长。
func (s *Store) Hint() string {
	return "[日记] 你的日记在 " + s.Dir + "，一天一个文件（如 " +
		time.Now().Format("2006-01-02") + ".md）。想回忆某天发生过什么，按日期去读；写就用 journal_write。"
}
