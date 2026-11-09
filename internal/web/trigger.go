package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentd/internal/hook"
	"agentd/internal/logx"
	"agentd/internal/store"
)

// InitHooks 装载触发器：读 hooks/*.json，并接上"叫醒一轮"的动作。
//
// 触发后写进一个固定的「观察」会话 —— 不碰用户正在聊的会话（主流做法都是这样，
// ChatGPT 的定时任务也是每个任务独立会话）。
func (s *Server) InitHooks(dir string) {
	m := hook.New(dir, func(ctx context.Context, d hook.Def, text string) error {
		conv, err := s.watchConv()
		if err != nil {
			return err
		}
		s.runTurn(ctx, conv, text, "trigger:"+d.Name)
		return nil
	})
	m.OnError(func(name string, err error) { logx.Warn("触发器 %s 执行失败: %v", name, err) })
	for _, w := range m.Load() {
		logx.Warn("触发器 %s", w)
	}
	s.hk = m
	logx.Info("触发器已装载 %d 个：%s", len(m.Names()), strings.Join(m.Names(), ", "))
}

// watchConv 找（或建）「观察」会话
func (s *Server) watchConv() (int64, error) {
	list, err := s.st.List()
	if err != nil {
		return 0, err
	}
	for _, c := range list {
		if c.Kind == "watch" {
			id := int64(0)
			fmt.Sscan(c.ID, &id)
			return id, nil
		}
	}
	c := &store.Conversation{Title: "观察", Kind: "watch"}
	if err := s.st.Save(c); err != nil {
		return 0, err
	}
	id := int64(0)
	fmt.Sscan(c.ID, &id)
	return id, nil
}

// POST /api/trigger  {"hook":"名字","data":{…}}
//
// 给外部脚本用：cron / inotifywait / systemd timer / 你自己的 shell 都行，
// 判断在哪醒不归 agent 管，它只负责"被叫醒之后做什么"。
func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "只支持 POST"})
		return
	}
	var in struct {
		Hook string         `json:"hook"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	if strings.TrimSpace(in.Hook) == "" {
		writeJSON(w, 400, map[string]string{"error": "需要 hook 名字"})
		return
	}
	if s.hk == nil {
		writeJSON(w, 503, map[string]string{"error": "触发器没启用"})
		return
	}
	// 触发是"发起一轮"：Trigger 内部异步叫醒，这里立刻返回
	msg, err := s.hk.Trigger(context.Background(), in.Hook, in.Data)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	logx.Info("trigger hook=%s data=%v -> %s", in.Hook, in.Data, msg)
	writeJSON(w, 200, map[string]any{"ok": true, "msg": msg})
}

// watchConvID 给 CLI/调试用
func (s *Server) WatchConvID() int64 {
	id, err := s.watchConv()
	if err != nil {
		return 0
	}
	return id
}
