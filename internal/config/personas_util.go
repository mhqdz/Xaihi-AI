package config

import (
	"fmt"
	"regexp"
)

var reNameOK = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func errBadPersonaName(n string) error {
	return fmt.Errorf("人格名只能用 a-z0-9_-（最多 32 字）：%q", n)
}

func errEmptyPersona() error { return fmt.Errorf("人格内容不能为空") }
