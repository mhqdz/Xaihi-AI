package tools

import (
	"xaihi/internal/config"
	"xaihi/internal/toolmeta"
)

// ForProfile 是运行时工具授权的唯一入口：发送请求与上下文估算必须共用。
// 空 Tools 表示不提供工具；人格、persistent、vision 约束在运行时再检查一次，
// 防止只改配置校验而遗漏 handoff、旧配置或直接构造 Profile 的路径。
func ForProfile(reg map[string]*Spec, p config.Profile) map[string]*Spec {
	if len(p.Tools) == 0 {
		return map[string]*Spec{}
	}
	selected := make(map[string]bool, len(p.Tools))
	for _, name := range p.Tools {
		selected[name] = true
	}
	var persona config.PersonaProfile
	var personaErr error
	personaReady := false
	for name, spec := range reg {
		if !selected[name] {
			delete(reg, name)
			continue
		}
		group := spec.Category
		if group == "" {
			group = toolmeta.Category(name)
		}
		if toolmeta.NeedsPersistent(group) && !p.Persistent {
			delete(reg, name)
			continue
		}
		if toolmeta.NeedsVision(group) && !hasInput(p.Input, "image") {
			delete(reg, name)
			continue
		}
		if toolmeta.NeedsPersona(group) {
			if p.Persona == "" {
				delete(reg, name)
				continue
			}
			if !personaReady {
				persona, personaErr = config.ResolvePersonaProfile(p.Persona, p.Persistent)
				personaReady = true
			}
			if personaErr != nil {
				delete(reg, name)
				continue
			}
			switch toolmeta.Capability(name) {
			case toolmeta.CapabilityMemory:
				if !persona.Memory {
					delete(reg, name)
				}
			case toolmeta.CapabilityWiki:
				if !persona.Wiki {
					delete(reg, name)
				}
			}
		}
	}
	return reg
}

func hasInput(input []string, want string) bool {
	for _, x := range input {
		if x == want {
			return true
		}
	}
	return false
}
