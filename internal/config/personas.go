package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 人格是**可插拔的文本**：personas/<名字>.md，profile 用 `persona` 字段引用。
// 这样多个 profile（不同模型）可以共用同一个人格，记忆才是"共同的"；
// 不引用人格的 profile（比如纯执行用的）就不会把她的记忆带进去。
//
// 人格和任务指令是两段：人格在最前（稳定），profile.system 跟在后面（可变）。

func PersonasDir() string { return filepath.Join(DataDir(), "personas") }

// LoadPersona 读一份人格；名字为空返回空串（= 不用人格，退化成普通 profile）。
func LoadPersona(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(PersonasDir(), name+".md"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ListPersonas 列出可用人格（按名字排序）
func ListPersonas() []string {
	des, err := os.ReadDir(PersonasDir())
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(des))
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
			continue
		}
		out = append(out, strings.TrimSuffix(de.Name(), ".md"))
	}
	sort.Strings(out)
	return out
}

// SavePersona 写一份人格（目录由 xaihi 维护，名字限 a-z0-9_-）
func SavePersona(name, body string) error {
	if !reNameOK.MatchString(name) {
		return errBadPersonaName(name)
	}
	if strings.TrimSpace(body) == "" {
		return errEmptyPersona()
	}
	if err := os.MkdirAll(PersonasDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(PersonasDir(), name+".md"), []byte(strings.TrimSpace(body)+"\n"), 0o644)
}
