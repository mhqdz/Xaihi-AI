package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// PersonaProfile 是人格自己的配置，和 profiles.json 里的会话/模型 Profile 分开。
// 会话 Profile 用 persona 引用它；引用同一人格的多个会话 Profile 共享 memory/wiki。
type PersonaProfile struct {
	Name    string `json:"name"`
	Memory  bool   `json:"memory"`
	Wiki    bool   `json:"wiki"`
	Dreamer string `json:"dreamer,omitempty"` // 引用 profiles.json 中的会话/模型 profile
	Exists  bool   `json:"-"`                 // 配置文件是否真实存在；仅用于兼容旧 persistent 配置
}

func PersonaProfilePath(name string) string {
	return filepath.Join(PersonasDir(), name+".json")
}

// LoadPersonaProfile 只读取人格 JSON。文件不存在不是错误：调用方可决定如何兼容旧配置。
func LoadPersonaProfile(name string) (PersonaProfile, error) {
	name = strings.TrimSpace(name)
	if !reNameOK.MatchString(name) {
		return PersonaProfile{}, errBadPersonaName(name)
	}
	p := PersonaProfile{Name: name}
	b, err := os.ReadFile(PersonaProfilePath(name))
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return PersonaProfile{}, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return PersonaProfile{}, fmt.Errorf("解析人格配置 %s：%w", name, err)
	}
	p.Name = name
	p.Exists = true
	return p, nil
}

// ResolvePersonaProfile 兼容旧结构：人格 JSON 不存在时，memory 跟随会话 profile 的 persistent。
// wiki 没有旧语义，默认关闭。人格 JSON 一旦存在，就完全以人格自己的配置为准。
func ResolvePersonaProfile(name string, legacyPersistent bool) (PersonaProfile, error) {
	p, err := LoadPersonaProfile(name)
	if err != nil {
		return PersonaProfile{}, err
	}
	if !p.Exists {
		p.Memory = legacyPersistent
		p.Wiki = false
	}
	return p, nil
}

// PersonaProfileFor 给不携带具体会话 profile 的 API 使用：
// 只要还有一个旧会话 profile 以 persistent=true 引用此人格，就保持旧 memory 可用。
func PersonaProfileFor(name string) (PersonaProfile, error) {
	legacy := false
	for _, p := range LoadProfiles() {
		if p.Persona == name && p.Persistent {
			legacy = true
			break
		}
	}
	return ResolvePersonaProfile(name, legacy)
}

// DreamerProfile 解析并校验 dreaming 使用的模型配置；不继承其中的人格、工具或记忆注入。
func (p PersonaProfile) DreamerProfile() (Profile, error) {
	name := strings.TrimSpace(p.Dreamer)
	if name == "" {
		return Profile{}, fmt.Errorf("人格 %s 未配置 dreamer", p.Name)
	}
	sp, ok := GetProfile(name)
	if !ok {
		return Profile{}, fmt.Errorf("人格 %s 的 dreamer 引用了不存在的 profile：%s", p.Name, name)
	}
	r := sp.Resolve()
	if strings.TrimSpace(r.ModelID) == "" || strings.TrimSpace(r.BaseURL) == "" {
		return Profile{}, fmt.Errorf("dreamer profile %s 缺少模型或接口地址", name)
	}
	return sp, nil
}

// SavePersonaProfile 原子写入人格 JSON；修改已有配置前保留一份 .bak。
func SavePersonaProfile(p PersonaProfile) error {
	p.Name = strings.TrimSpace(p.Name)
	if !reNameOK.MatchString(p.Name) {
		return errBadPersonaName(p.Name)
	}
	if _, err := LoadPersona(p.Name); err != nil {
		return fmt.Errorf("找不到人格正文 %s：%w", p.Name, err)
	}
	p.Dreamer = strings.TrimSpace(p.Dreamer)
	if p.Dreamer != "" {
		if _, err := p.DreamerProfile(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(PersonasDir(), 0o755); err != nil {
		return err
	}
	path := PersonaProfilePath(p.Name)
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	if old, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
			return fmt.Errorf("备份人格配置失败：%w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	p.Exists = false // 运行时字段，不序列化
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(PersonasDir(), ".persona-profile-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
