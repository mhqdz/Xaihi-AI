// Package toolmeta：工具的分组与"需要什么能力"。
//
// 分组名直接用中文，也是界面上的分组标题 —— 不再维护"英文 key → 中文 label"的映射表，
// 少一层翻译就少一处不同步。中文在 Go 源码 / JSON / HTTP 里都是 UTF-8，没有编码问题
// （唯一要注意的是别拿它当文件名或 URL 参数，这里不涉及）。
//
// 分组不是固定枚举：插件可以声明自己的分组名（见 tools.PluginDraft.Category），
// 界面按"出现过的分组"动态渲染，没见过的排在预置顺序之后。
package toolmeta

import "strings"

const (
	GroupChat      = "会话"  // bash / recall / spawn_task
	GroupKnowledge = "知识"  // kb_* / web_*
	GroupView      = "视图"  // screenshot / view_image
	GroupPersona   = "人格"  // memory_* / wiki_*：归属人格 profile
	GroupPersist   = "持久化" // hook_* / journal_*：旧 persistent 配置
	GroupTodo      = "待办"  // todo_*：她记着"该提醒你什么"
	GroupExt       = "扩展"  // 插件没声明分组时的兜底
)

const (
	CapabilityNone   = ""
	CapabilityMemory = "memory"
	CapabilityWiki   = "wiki"
)

// order：预置分组的展示顺序；未知分组排在它们后面（按名字排序）。
var order = []string{GroupChat, GroupKnowledge, GroupView, GroupPersona, GroupPersist, GroupTodo, GroupExt}

// Category 按工具名归类
func Category(name string) string {
	switch {
	case strings.HasPrefix(name, "memory_"), strings.HasPrefix(name, "wiki_"):
		return GroupPersona
	}
	switch name {
	case "hook_add", "hook_list", "hook_rm", "journal_write":
		return GroupPersist
	case "todo_add", "todo_list", "todo_done":
		return GroupTodo
	case "screenshot", "view_image":
		return GroupView
	case "kb_search", "kb_get", "web_search", "web_fetch":
		return GroupKnowledge
	case "bash", "recall", "spawn_task":
		return GroupChat
	}
	return GroupExt
}

// Order 给前端排序用：返回预置顺序里分的下标（未知分组返回 -1）
func Order(group string) int {
	for i, g := range order {
		if g == group {
			return i
		}
	}
	return -1
}

// NeedsPersistent 这一组要会话 profile 打开旧「持久化会话」开关。
func NeedsPersistent(group string) bool { return group == GroupPersist || group == GroupTodo }

// NeedsPersona 这一组必须引用人格；memory/wiki 还要人格 profile 分别开启对应能力。
func NeedsPersona(group string) bool { return group == GroupPersona }

// NeedsVision 这一组要模型能接收图像
func NeedsVision(group string) bool { return group == GroupView }

func Capability(name string) string {
	switch {
	case strings.HasPrefix(name, "memory_"):
		return CapabilityMemory
	case strings.HasPrefix(name, "wiki_"):
		return CapabilityWiki
	default:
		return CapabilityNone
	}
}

// 兼容旧调用点：按工具名判断（内部转成按分组）
func NeedsPersistentTool(name string) bool { return NeedsPersistent(Category(name)) }
func NeedsPersonaTool(name string) bool    { return NeedsPersona(Category(name)) }
func NeedsVisionTool(name string) bool     { return NeedsVision(Category(name)) }
