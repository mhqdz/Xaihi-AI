package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"time"
	"xaihi/internal/toolmeta"
)

// 外置插件：~/.data/xaihi/tools.d/*.json
//   {
//     "name": "kb_get",
//     "description": "…",
//     "params": { "id": {"type":"string"} },
//     "required": ["id"],
//     "command": ["~/.local/bin/kb", "get", "{{id}}"],   // 直接 exec，不经 shell（无注入风险）
//     "timeoutMs": 20000,
//     "env": {"FOO":"bar"}
//   }
// 参数替换：argv 元素里的 {{name}} 用该参数值替换；元素只由占位符构成且值来自数组时，展开为多个 argv。
// 输出：stdout 纯文本；或 JSON {"text": "...", "images": ["data:..."]}（与内置工具的 content parts 对齐）。

type manifest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Params      map[string]any    `json:"params"`
	Required    []string          `json:"required"`
	Command     []string          `json:"command"`
	TimeoutMs   int               `json:"timeoutMs"`
	Persistent  bool              `json:"persistent"` // true = 常驻进程，JSON-RPC over stdio
	Env         map[string]string `json:"env"`
	// Category：分组名（中文，例如「绘图」）。留空 → 「扩展」。
	Category string `json:"category,omitempty"`
}

// LoadPlugins 读取插件目录，返回可直接使用的 Spec（Run 走一次性进程调用）。
func LoadPlugins(dir string) (map[string]*Spec, []string) {
	out := map[string]*Spec{}
	var warns []string
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out, nil
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		var m manifest
		if err := json.Unmarshal(b, &m); err != nil {
			warns = append(warns, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		if strings.TrimSpace(m.Name) == "" || len(m.Command) == 0 {
			warns = append(warns, fmt.Sprintf("%s: 缺少 name 或 command", e.Name()))
			continue
		}
		if m.Persistent {
			p, tl, err := ensureProc(m, path)
			if err != nil {
				warns = append(warns, fmt.Sprintf("%s: 常驻插件启动失败: %v", e.Name(), err))
				continue
			}
			for _, t := range tl {
				name := t.Name
				if name == "" {
					name = m.Name
				}
				params := t.Params
				if len(params) == 0 {
					params = m.Params
				}
				req := t.Required
				if len(req) == 0 {
					req = m.Required
				}
				desc := t.Description
				if desc == "" {
					desc = m.Description
				}
				proc, toolName := p, name
				cat := strings.TrimSpace(m.Category)
				if cat == "" {
					cat = toolmeta.GroupExt
				}
				out[name] = &Spec{
					Name: name, Category: cat, Description: desc, Params: params, Required: req,
					Source: "plugin", Path: path,
					Run: func(ctx context.Context, args map[string]any, _ *Env) (any, error) {
						for _, k := range req {
							if args[k] == nil || strings.TrimSpace(fmt.Sprint(args[k])) == "" {
								return "[error] 缺少参数 " + k, nil
							}
						}
						return proc.callTool(ctx, toolName, args)
					},
				}
			}
			continue
		}
		cat := strings.TrimSpace(m.Category)
		if cat == "" {
			cat = toolmeta.GroupExt
		}
		sp := &Spec{
			Name:        m.Name,
			Category:    cat,
			Description: m.Description,
			Params:      m.Params,
			Required:    m.Required,
			Source:      "plugin",
			Path:        path,
			Command:     m.Command,
			TimeoutMs:   m.TimeoutMs,
		}
		sp.Run = pluginRun(m)
		out[m.Name] = sp
	}
	return out, warns
}

func pluginRun(m manifest) func(ctx context.Context, args map[string]any, env *Env) (any, error) {
	return func(ctx context.Context, args map[string]any, _ *Env) (any, error) {
		// 必填参数先校验，不白起进程
		for _, k := range m.Required {
			if strings.TrimSpace(fmt.Sprint(args[k])) == "" || args[k] == nil {
				return "[error] 缺少参数 " + k, nil
			}
		}
		argv, err := expandArgv(m.Command, args)
		if err != nil {
			return "[error] " + err.Error(), nil
		}
		argv[0] = expandHome(argv[0])
		timeout := time.Duration(m.TimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		if timeout > 10*time.Minute {
			timeout = 10 * time.Minute
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
		cmd.Env = append(os.Environ(), argEnv(m.Name, args)...) // 继承 HOME/PATH 等，再追加工具参数
		for k, v := range m.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		// stdin：完整请求 JSON（脚本想用结构化数据时读它；简单脚本读 XAIHI_ARG_* 即可）
		if b, err := json.Marshal(map[string]any{"tool": m.Name, "args": args}); err == nil {
			cmd.Stdin = bytes.NewReader(b)
		}
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		runErr := cmd.Run()
		out := buf.String()
		if cctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("[timeout] %s 超过 %s\n%s", m.Name, timeout, clipOutput(out, 4000)), nil
		}
		if runErr != nil {
			code := 0
			if ee, ok := runErr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				return "[error] " + runErr.Error(), nil
			}
			return fmt.Sprintf("%s\n[exit %d]", clipOutput(out, 8000), code), nil
		}
		// 插件可以返回 JSON（带图片）或纯文本
		trimmed := strings.TrimSpace(out)
		if strings.HasPrefix(trimmed, "{") {
			var resp struct {
				Text   string   `json:"text"`
				Images []string `json:"images"`
			}
			if json.Unmarshal([]byte(trimmed), &resp) == nil && (resp.Text != "" || len(resp.Images) > 0) {
				if len(resp.Images) == 0 {
					return resp.Text, nil
				}
				parts := []map[string]any{{"type": "text", "text": resp.Text}}
				for _, img := range resp.Images {
					parts = append(parts, map[string]any{"type": "image", "url": img})
				}
				return parts, nil
			}
		}
		return clipOutput(out, 8000), nil
	}
}

// expandArgv 处理 {{name}} 占位符：单个占位符 + 数组值 → 展开成多个 argv。
func expandArgv(tmpl []string, args map[string]any) ([]string, error) {
	out := make([]string, 0, len(tmpl))
	for _, t := range tmpl {
		name, whole := placeholderName(t)
		if !whole {
			out = append(out, t)
			continue
		}
		v, ok := args[name]
		if !ok || v == nil {
			return nil, fmt.Errorf("缺少参数 %s", name)
		}
		switch vv := v.(type) {
		case []any:
			for _, e := range vv {
				s := strings.TrimSpace(fmt.Sprint(e))
				if s != "" {
					out = append(out, s)
				}
			}
		case []string:
			for _, s := range vv {
				if strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			}
		case bool:
			if vv {
				out = append(out, "true")
			} else {
				out = append(out, "false")
			}
		case float64:
			out = append(out, strconv.FormatFloat(vv, 'f', -1, 64))
		default:
			out = append(out, strings.TrimSpace(fmt.Sprint(vv)))
		}
	}
	return out, nil
}

func placeholderName(s string) (string, bool) {
	if !strings.HasPrefix(s, "{{") || !strings.HasSuffix(s, "}}") {
		return "", false
	}
	name := strings.TrimSpace(s[2 : len(s)-2])
	if name == "" || strings.ContainsAny(name, "{} ") {
		return "", false
	}
	return name, true
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// argEnv：把工具参数导出成 XAIHI_ARG_<NAME> 环境变量（数组用换行连接），
// 这样 shell 脚本插件不用自己解析 JSON，例如：
//
//	exec ~/.local/bin/kb search "$XAIHI_ARG_QUERY" -n "${XAIHI_ARG_MAX_RESULTS:-5}"
func argEnv(tool string, args map[string]any) []string {
	out := []string{"XAIHI_TOOL=" + tool}
	for k, v := range args {
		name := "XAIHI_ARG_" + strings.ToUpper(k)
		name = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(name)
		var val string
		switch vv := v.(type) {
		case []any:
			parts := make([]string, 0, len(vv))
			for _, e := range vv {
				parts = append(parts, fmt.Sprint(e))
			}
			val = strings.Join(parts, "\n")
		case []string:
			val = strings.Join(vv, "\n")
		case bool:
			val = strconv.FormatBool(vv)
		case float64:
			val = strconv.FormatFloat(vv, 'f', -1, 64)
		case nil:
			continue
		default:
			val = fmt.Sprint(vv)
		}
		out = append(out, name+"="+val)
	}
	return out
}
