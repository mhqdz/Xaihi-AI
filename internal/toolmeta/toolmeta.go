// Package toolmeta：工具的分组与"需要什么能力"。
//
// 分组名直接用中文，也是界面上的分组标题 —— 不再维护"英文 key → 中文 label"的映射表，
// 少一层翻译就少一处不同步。中文在 Go 源码 / JSON / HTTP 里都是 UTF-8，没有编码问题
// （唯一要注意的是别拿它当文件名或 URL 参数，这里不涉及）。
//
// 分组不是固定枚举：插件可以声明自己的分组名（见 tools.PluginDraft.Category），
// 界面按"出现过的分组"动态渲染，没见过的排在预置顺序之后。
package toolmeta

const (
	GroupChat      = "会话"  // bash / recall / spawn_task
	GroupKnowledge = "知识"  // kb_* / web_*
	GroupView      = "视图"  // screenshot / view_image
	GroupPersist   = "持久化" // memory_* / hook_*
	GroupExt       = "扩展"  // 插件没声明分组时的兜底
)

// order：预置分组的展示顺序；未知分组排在它们后面（按名字排序）。
var order = []string{GroupChat, GroupKnowledge, GroupView, GroupPersist, GroupExt}

// Category 按工具名归类
func Category(name string) string {
	switch name {
	case "memory_save", "memory_forget", "memory_list", "hook_add", "hook_list", "hook_rm":
		return GroupPersist
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

// NeedsPersistent 这一组要 profile 打开「持久化会话」
func NeedsPersistent(group string) bool { return group == GroupPersist }

// NeedsVision 这一组要模型能接收图像
func NeedsVision(group string) bool { return group == GroupView }

// 兼容旧调用点：按工具名判断（内部转成按分组）
func NeedsPersistentTool(name string) bool { return NeedsPersistent(Category(name)) }
func NeedsVisionTool(name string) bool     { return NeedsVision(Category(name)) }
