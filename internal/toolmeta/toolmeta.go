// Package toolmeta：工具的分类与"需要什么能力"。
//
// 单独成包是为了避免 tool → config → tool 的循环依赖：tools 用它给 Spec 打标签，
// config 用它校验 profile 里选中的工具是否说得通。
package toolmeta

const (
	Core   = "core"   // 常规工具：什么会话都能用
	Memory = "memory" // 长期记忆：要开「持久化会话」
	Hook   = "hook"   // 触发器：要开「持久化会话」
	Image  = "image"  // 给模型看图：要模型能接收图像
)

// Category 按工具名归类。插件没有特定名字，一律 core。
func Category(name string) string {
	switch name {
	case "memory_save", "memory_forget", "memory_list":
		return Memory
	case "hook_add", "hook_list", "hook_rm":
		return Hook
	case "screenshot", "view_image":
		return Image
	}
	return Core
}

// NeedsVision 这类工具要模型能接收图像（profile.input 含 image）
func NeedsVision(name string) bool { return Category(name) == Image }

// NeedsPersistent 这类工具要 profile 打开「持久化会话」
func NeedsPersistent(name string) bool {
	c := Category(name)
	return c == Memory || c == Hook
}

// NeedsPrompt：没有提示词就没有人格，记忆/钩子单靠工具也没意义
func NeedsPrompt(name string) bool { return Category(name) == Memory }
