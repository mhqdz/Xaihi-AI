package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

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
//	  {"kind":"endpoint","data":{...config.Endpoint...}},
//	  {"kind":"model","data":{...config.Model...}},
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
		idx        int // 在请求里的下标：报错时用它指认"第几项"
		run        func() error
	}
	ops := make([]op, 0, len(in.Changes))
	// 校验失败统一回报：前端靠 index 指出是哪一条，而不是只给一句"保存失败"
	reject := func(i int, c applyChange, msg string) {
		writeJSON(w, 400, map[string]any{
			"error": msg, "index": i, "kind": c.Kind, "name": c.Name, "applied": 0,
		})
	}

	// 预扫：本批次里新声明的接口和模型。
	//
	// 三层是引用关系（配置 → 模型 → 接口），而一张表单常常把三层一起提交
	// （新建接口 + 注册模型 + 配一个 profile）。校验"模型引用的接口存在吗"时，
	// 接口正躺在同一张表单里、还没落盘 —— 只看盘上有什么就会误报"没有这个接口"。
	// 所以先扫一遍，校验时把本批次的声明也当成存在。
	newEPs := map[string]bool{}
	for _, c := range in.Changes {
		if c.Kind != "endpoint" {
			continue
		}
		var e config.Endpoint
		if json.Unmarshal(c.Data, &e) == nil && e.Name != "" {
			newEPs[e.Name] = true
		}
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
			if err := config.ValidateProfileWith(p, newEPs); err != nil {
				reject(i, c, err.Error())
				return
			}
			saved := p // 显式拷贝：闭包别去抓循环变量
			ops = append(ops, op{"profile", p.Name, i, func() error { return config.UpsertProfile(saved) }})
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
			ops = append(ops, op{"tool", name, i, func() error {
				_, err := saveToolDescription(name, desc)
				return err
			}})
		case "plugin":
			// 整份插件定义（新建 / 改命令 / 改名 / 改参数）。校验在 SavePlugin 里，
			// 这里只做解析 —— 保证它和"单发 POST /api/plugins"走同一条路。
			var d tools.PluginDraft
			if err := json.Unmarshal(c.Data, &d); err != nil {
				reject(i, c, "plugin 解析失败："+err.Error())
				return
			}
			saved := d
			ops = append(ops, op{"plugin", d.Name, i, func() error {
				if err := tools.SavePlugin(saved); err != nil {
					return err
				}
				// 和单发 POST /api/plugins 一致：改名后把各 profile 的引用换过来
				if orig := strings.TrimSpace(saved.OldName); orig != "" && orig != saved.Name {
					if _, err := config.RenameToolInProfiles(orig, saved.Name); err != nil {
						logx.Warn("plugin.rename sync profiles: %v", err)
					}
				}
				return nil
			}})
		case "endpoint":
			// 接口（地址 + 凭据 + 协议）。名字是模型引用它的键，改名等于新建，
			// 所以界面上只在新建时可改 —— 这里不做改名级联。
			var e config.Endpoint
			if err := json.Unmarshal(c.Data, &e); err != nil {
				reject(i, c, "endpoint 解析失败："+err.Error())
				return
			}
			// 第一阶段就校验：不然缺地址会拖到写盘时变成 500，调用方分不清
			// "我填错了"和"服务器坏了"
			if err := config.ValidateEndpoint(e); err != nil {
				reject(i, c, err.Error())
				return
			}
			saved := e
			ops = append(ops, op{"endpoint", e.Name, i, func() error { return config.UpsertEndpoint(saved) }})
		case "settings":
			var st config.Settings
			if err := json.Unmarshal(c.Data, &st); err != nil {
				reject(i, c, "settings 解析失败："+err.Error())
				return
			}
			saved := st
			ops = append(ops, op{"settings", "", i, func() error { return config.SaveSettings(saved) }})
		default:
			reject(i, c, "未知的改动类型："+c.Kind)
			return
		}
	}

	// ── 第二阶段：写 ──
	//
	// 按依赖排序再写：接口 → 配置 → 其它。
	// 配置引用接口，同一张表单里常常一起提交；照着请求顺序写的话，
	// "先配置后接口"这种顺序就会在 UpsertProfile 里撞到"没有这个接口"。
	// 排序只影响写盘顺序，报错里的"第几项"仍然按请求下标说。
	prio := map[string]int{"endpoint": 0, "profile": 1}
	rank := func(k string) int {
		if p, ok := prio[k]; ok {
			return p
		}
		return 3
	}
	sort.SliceStable(ops, func(a, b int) bool { return rank(ops[a].kind) < rank(ops[b].kind) })

	before := config.LoadSettings()
	for n, o := range ops {
		if err := o.run(); err != nil {
			writeJSON(w, 500, map[string]any{
				"error": "第 " + strconv.Itoa(o.idx+1) + " 项（" + o.kind + " " + o.name + "）写入失败：" + err.Error(),
				"index": o.idx, "kind": o.kind, "name": o.name, "applied": n,
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
