package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"agentd/internal/config"
	"agentd/internal/logx"
	"agentd/internal/tools"
)

// POST /api/apply —— 一次提交若干处改动。
//
// 以前是前端循环发请求：每个 profile 一个 POST、工具说明一个 PUT、常规配置再一个 PUT。
// 前端得知道每种改动该打到哪个端点，而且中途失败就停在半路，前面几条已经生效，
// 调用方很难说清"到底存了什么"。
//
// 现在前端只发一张表单，这里解析 kind 再分发：
//
//	{"changes":[
//	  {"kind":"profile","data":{...config.Profile...}},
//	  {"kind":"tool","name":"kb_get","data":{"description":"..."}},
//	  {"kind":"settings","data":{...config.Settings...}}
//	]}
//
// 分两阶段：先把每一项解析并校验完（一个问题都不写盘），全部通过才开始写。
// 这样"第一条写成功、第二条字段非法"这种半成品不会出现。
// 第二阶段只剩 IO 错误，真发生就返回已写入的条数，不假装整体成功。
type applyChange struct {
	Kind string          `json:"kind"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	var in struct {
		Changes []applyChange `json:"changes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if len(in.Changes) == 0 {
		writeJSON(w, 400, map[string]string{"error": "changes 为空"})
		return
	}
	if len(in.Changes) > 200 {
		writeJSON(w, 400, map[string]string{"error": "一次最多 200 项"})
		return
	}

	type op struct {
		kind, name string
		run        func() error
	}
	ops := make([]op, 0, len(in.Changes))
	// 校验失败统一回报：前端靠 index 指出是哪一条，而不是只给一句"保存失败"
	reject := func(i int, c applyChange, msg string) {
		writeJSON(w, 400, map[string]any{
			"error": msg, "index": i, "kind": c.Kind, "name": c.Name, "applied": 0,
		})
	}

	// ── 第一阶段：解析 + 校验，不落盘 ──
	for i, c := range in.Changes {
		switch c.Kind {
		case "profile":
			var p config.Profile
			if err := json.Unmarshal(c.Data, &p); err != nil {
				reject(i, c, "profile 解析失败："+err.Error())
				return
			}
			if err := config.ValidateProfile(p); err != nil {
				reject(i, c, err.Error())
				return
			}
			saved := p // 显式拷贝：闭包别去抓循环变量
			ops = append(ops, op{"profile", p.Name, func() error { return config.UpsertProfile(saved) }})
		case "tool":
			name := c.Name
			var body struct {
				Description string `json:"description"`
			}
			if err := json.Unmarshal(c.Data, &body); err != nil {
				reject(i, c, "tool 解析失败："+err.Error())
				return
			}
			if _, ok := tools.Registry()[name]; !ok {
				reject(i, c, "没有这个工具："+name)
				return
			}
			desc := body.Description
			ops = append(ops, op{"tool", name, func() error {
				_, err := saveToolDescription(name, desc)
				return err
			}})
		case "settings":
			var st config.Settings
			if err := json.Unmarshal(c.Data, &st); err != nil {
				reject(i, c, "settings 解析失败："+err.Error())
				return
			}
			saved := st
			ops = append(ops, op{"settings", "", func() error { return config.SaveSettings(saved) }})
		default:
			reject(i, c, "未知的改动类型："+c.Kind)
			return
		}
	}

	// ── 第二阶段：写 ──
	before := config.LoadSettings()
	for i, o := range ops {
		if err := o.run(); err != nil {
			writeJSON(w, 500, map[string]any{
				"error": "第 " + strconv.Itoa(i+1) + " 项（" + o.kind + " " + o.name + "）写入失败：" + err.Error(),
				"index": i, "kind": o.kind, "name": o.name, "applied": i,
			})
			return
		}
	}
	// 和单发 PUT /api/settings 保持一致：归档配置变了就立刻巡检一次
	after := config.LoadSettings()
	if before.AutoArchiveEnabled != after.AutoArchiveEnabled ||
		before.AutoArchiveDays != after.AutoArchiveDays {
		logx.Info("settings.auto_archive enabled=%v days=%d", after.AutoArchiveEnabled, after.AutoArchiveDays)
		go s.sweepAutoArchive()
	}
	writeJSON(w, 200, map[string]any{"ok": true, "applied": len(ops)})
}
