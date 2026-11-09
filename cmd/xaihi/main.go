// xaihi：本机 agent 的骨架。P0 = 多轮对话（无工具、无 UI）。
//
//	xaihi chat                 交互式多轮（Ctrl-D 退出）
//	xaihi send "一句话"         单轮（可带 -conv <id> 续用会話）
//	xaihi convs                列出会话
//	xaihi show <id>            打印会话全文
//	xaihi config               打印生效的配置
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"xaihi/internal/agent"
	"xaihi/internal/config"
	"xaihi/internal/hook"
	"xaihi/internal/llm"
	"xaihi/internal/logx"
	"xaihi/internal/store"
	"xaihi/internal/tools"
	"xaihi/internal/watch"
	"xaihi/internal/web"
)

var version = "0.0.1-p0"

func main() {
	cfg := config.Load()
	// 日志：写 <data_dir>/xaihi.log（同时打 stderr）
	if err := logx.Init(cfg.DataDir); err != nil {
		fmt.Fprintf(os.Stderr, "日志初始化失败：%v（仅 stderr）\n", err)
	}
	logx.Info("start model=%s base_url=%s data_dir=%s", cfg.Model, cfg.BaseURL, cfg.DataDir)

	st, err := store.NewSQLite(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化存储失败:", err)
		os.Exit(1)
	}
	// 上次进程若在流式中被 kill，库里会留下 status=running 的消息；启动时统一标成 failed
	if n, err := st.MarkRunningFailed(); err != nil {
		logx.Warn("扫描未完成的流式消息失败: %v", err)
	} else if n > 0 {
		logx.Info("已把 %d 条上次中断的流式消息标为 failed", n)
	}
	pname := "180-think"
	for i, x := range os.Args {
		if x == "--profile" && i+1 < len(os.Args) {
			pname = os.Args[i+1]
		}
	}
	if v := os.Getenv("XAIHI_PROFILE"); v != "" {
		pname = v
	}
	prof, ok := config.GetProfile(pname)
	if !ok {
		list := config.LoadProfiles()
		if len(list) == 0 {
			fmt.Fprintln(os.Stderr, "✗ 没有任何 profile")
			os.Exit(2)
		}
		prof = list[0]
		fmt.Fprintf(os.Stderr, "（没有 %q，改用 %q）\n", pname, prof.Name)
	}
	a := agent.New(cfg, st, prof)

	args := os.Args[1:]
	cmd := "chat"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve":
		addr := "127.0.0.1:18400"
		if v := os.Getenv("XAIHI_ADDR"); v != "" {
			addr = v
		}
		// 迁移，顺序不能反：先把老 profile 里的地址抽成接口，
		// 再把三层的模型条目收回两层（裸 id + 能力进 profile）。
		// 两个都可重复执行，已迁移就是空操作。
		if created, changed := config.MigrateEndpoints(); created > 0 {
			logx.Info("接口迁移：新建 %d 个，改了 %d 个配置", created, changed)
		}
		if changed, eps := config.MigrateBackLayers(); changed > 0 {
			logx.Info("回两层迁移：改了 %d 个配置，%d 个接口的能力已归档", changed, eps)
		}
		s := web.New(cfg, a, st)
		if err := s.InitHandoff(cfg.DataDir); err != nil {
			logx.Warn("handoff 初始化失败: %v", err)
		}
		// 触发器：外部脚本调 /api/trigger，内置观察器也走同一个入口
		hooks := s.NewHooks(config.DataPath("hooks"))
		s.SetHooks(hooks)
		hooks.OnReload(func(names []string) {
			logx.Info("触发器已重载 %d 个：%s", len(names), strings.Join(names, ", "))
		})
		go hooks.WatchDir(context.Background(), 5*time.Second)
		// 能力缓存只在打开下拉/点刷新时更新，长期不开面板的配置会一直用很旧的数字：
		// 启动后台补一次过期的（不阻塞启动，失败只记日志）
		go s.WarmModelCaches(24 * time.Hour)

		// 观察器：只记"她在用什么应用"（内部不调模型）。没装 hyprctl 时
		// 采样失败是常态，Run 会自己跳过这一轮，不影响服务。
		if ws, err := watch.Open(config.DataPath("watch.db")); err == nil {
			w := &watch.Watcher{Store: ws, Samp: watch.Hypr{}}
			w.OnClose = func(win watch.Win) {
				// 只在用户定义了 app-close 钩子时才触发（否则每次关窗都报一句"没有这个钩子"）
				if !hooks.Has("app-close") {
					return
				}
				if _, err := hooks.Trigger(context.Background(), "app-close",
					map[string]any{"app": win.Class, "title": win.Title, "pid": win.PID}); err != nil {
					logx.Info("app-close 触发被拒: %v", err)
				}
			}
			go w.Run(context.Background())
			go w.WatchWindows(context.Background())
			tools.SetShotQuota(func() (bool, int) {
				ok, used, _ := ws.TakeShot(300) // 一天最多 300 张（你定的）
				return ok, used
			})
			logx.Info("观察器已启动（事件表 watch.db，截图配额 300/天）")
		} else {
			logx.Warn("观察器初始化失败: %v", err)
		}
		// 触发端点只挂在 unix socket 上（TCP 端口没鉴权，不能给它加"让 agent 干活"的入口）
		sock := config.DataPath("xaihi.sock")
		_ = os.Remove(sock) // 上次异常退出的残留
		ln, err := net.Listen("unix", sock)
		if err != nil {
			logx.Warn("触发 socket 起不来（trigger 命令将不可用）: %v", err)
		} else {
			_ = os.Chmod(sock, 0o600)
			go func() {
				if err := http.Serve(ln, s.TriggerMux()); err != nil && err != http.ErrServerClosed {
					logx.Warn("触发 socket 退出: %v", err)
				}
			}()
			logx.Info("触发端点 %s（仅本地 socket）", sock)
		}

		srv := &http.Server{Addr: addr, Handler: s.Handler()}
		fmt.Fprintf(os.Stderr, "xaihi %s · model=%s · http://%s\n", version, cfg.Model, addr)
		// 优雅停机：先把进行中的轮次取消（它们会落库半截内容 + 中断说明），再退出
		go func() {
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
			<-ch
			tools.ShutdownPlugins()
			if n := s.CancelAll(); n > 0 {
				logx.Info("shutdown cancel_running=%d", n)
				time.Sleep(3 * time.Second)
			}
			os.Exit(0)
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal(err)
		}
	case "hook":
		dir := config.DataPath("hooks")
		sub := ""
		if len(args) > 0 {
			sub = args[0]
		}
		switch sub {
		case "list", "":
			defs, _ := hook.LoadDir(dir)
			if len(defs) == 0 {
				fmt.Println("（还没有钩子）")
				return
			}
			for _, d := range defs {
				fmt.Printf("%-16s %-7s 配额 %d/天  冷却 %ds  %s\n",
					d.Name, d.Mode, d.QuotaOrDefault(), d.CooldownOrDefault(), oneLine(d.Prompt))
			}
		case "add":
			if len(args) < 2 {
				fatal(fmt.Errorf("用法: xaihi hook add <名字> --prompt \"…\" [--mode speak|silent] [--quota 10] [--cooldown 3]"))
			}
			name := args[1]
			d := hook.Def{Name: name, Mode: "speak"}
			for i := 2; i < len(args); i++ {
				val := func(k string) string {
					if i+1 < len(args) {
						return args[i+1]
					}
					return ""
				}
				switch args[i] {
				case "--prompt", "-p":
					d.Prompt = val("")
					i++
				case "--mode":
					d.Mode = val("")
					i++
				case "--quota":
					fmt.Sscanf(val(""), "%d", &d.QuotaPerDay)
					i++
				case "--cooldown":
					fmt.Sscanf(val(""), "%d", &d.CooldownS)
					i++
				}
			}
			if err := hook.Write(dir, d); err != nil {
				fatal(err)
			}
			fmt.Printf("已写入钩子 %s（%s，配额 %d/天）—— 5 秒内自动生效\n", d.Name, d.Mode, d.QuotaOrDefault())
		case "rm", "remove":
			if len(args) < 2 {
				fatal(fmt.Errorf("用法: xaihi hook rm <名字>"))
			}
			if err := hook.Remove(dir, args[1]); err != nil {
				fatal(err)
			}
			fmt.Printf("已删除钩子 %s（5 秒内生效）\n", args[1])
		default:
			fatal(fmt.Errorf("用法: xaihi hook [list | add <名字> --prompt … | rm <名字>]"))
		}
	case "trigger":
		if len(args) == 0 {
			fatal(fmt.Errorf("用法: xaihi trigger <钩子名> [--data '{\"k\":\"v\"}']"))
		}
		name := args[0]
		data := map[string]any{}
		for i := 1; i < len(args); i++ {
			if (args[i] == "--data" || args[i] == "-d") && i+1 < len(args) {
				if err := json.Unmarshal([]byte(args[i+1]), &data); err != nil {
					fatal(fmt.Errorf("--data 不是合法 JSON: %v", err))
				}
				i++
			}
		}
		sock := os.Getenv("XAIHI_SOCK")
		if sock == "" {
			sock = config.DataPath("xaihi.sock")
		}
		body, _ := json.Marshal(map[string]any{"hook": name, "data": data})
		// 走 unix socket：触发是"让 agent 干活"，不开 TCP 口子
		cl := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}}
		resp, err := cl.Post("http://unix/api/trigger", "application/json", bytes.NewReader(body))
		if err != nil {
			fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		fmt.Println(strings.TrimSpace(string(out)))
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
	case "version":
		fmt.Println("xaihi " + version)
	case "config":
		fmt.Printf("base_url  %s\nmodel     %s\ndata_dir  %s\n", cfg.BaseURL, cfg.Model, cfg.DataDir)
	case "convs":
		list, err := st.List()
		if err != nil {
			fatal(err)
		}
		for _, c := range list {
			fmt.Printf("%s  %s  %s\n", c.ID, c.UpdatedAt.Format("2006-01-02 15:04"), c.Title)
		}
	case "archive", "restore", "rm":
		if len(args) == 0 {
			fatal(fmt.Errorf("用法: xaihi %s <id>", cmd))
		}
		var err error
		switch cmd {
		case "archive":
			err = st.Archive(args[0])
		case "restore":
			err = st.Restore(args[0])
		case "rm":
			hard := false
			for _, a := range args[1:] {
				if a == "--hard" {
					hard = true
				}
			}
			err = st.Delete(args[0], hard)
		}
		if err != nil {
			fatal(err)
		}
		fmt.Println("✓", cmd, args[0])
	case "archived":
		list, err := st.ListArchived()
		if err != nil {
			fatal(err)
		}
		for _, c := range list {
			fmt.Printf("%s  %s  %s\n", c.ID, c.UpdatedAt.Format("2006-01-02 15:04"), c.Title)
		}
	case "show":
		if len(args) == 0 {
			fatal(fmt.Errorf("用法: xaihi show <id>"))
		}
		c, err := st.Get(args[0])
		if err != nil {
			fatal(err)
		}
		fmt.Printf("# %s (%s)\n", c.Title, c.ID)
		for _, m := range c.Messages {
			fmt.Printf("\n[%s]\n%s\n", m.Role, m.Content)
		}
	case "send":
		if len(args) == 0 {
			fatal(fmt.Errorf(`用法: xaihi send "一句话" [-conv id]`))
		}
		text, convID := args[0], ""
		for i := 1; i < len(args); i++ {
			if args[i] == "-conv" && i+1 < len(args) {
				convID = args[i+1]
				i++
			}
		}
		c, err := a.Conversation(convID)
		if err != nil {
			fatal(err)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		err = a.Send(ctx, c, text, func(content, reasoning string) {
			if content != "" {
				fmt.Print(content)
			}
		})
		fmt.Println()
		if err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "\n[conv %s]\n", c.ID)
	case "chat":
		c, err := a.Conversation("")
		if err != nil {
			fatal(err)
		}
		fmt.Printf("xaihi %s · model=%s · conv=%s\n（空行退出；Ctrl-D 结束）\n", version, cfg.Model, c.ID)
		sc := bufio.NewScanner(os.Stdin)
		// 默认 64KB 的行上限：粘贴一大段日志就会让 Scan() 返回 false，
		// 而下面只写了 break —— 表现是"打完字回车，程序直接退出，什么都没说"。
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for {
			fmt.Print("\n> ")
			if !sc.Scan() {
				if err := sc.Err(); err != nil {
					fmt.Fprintln(os.Stderr, "✗ 读取输入失败：", err)
				}
				break
			}
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				break
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			start := time.Now()
			err := a.Send(ctx, c, line, func(content, reasoning string) {
				if content != "" {
					fmt.Print(content)
				}
			})
			cancel()
			fmt.Printf("\n[%.1fs]\n", time.Since(start).Seconds())
			if err != nil {
				fmt.Fprintln(os.Stderr, "✗", err)
			}
		}
		// 一句话都没说就退出时 c.ID 是空的（Conversation 只建对象、没落库）——
		// 这时候说"已保存"是假话，xaihi show 也拿不到东西。
		if c.ID == "" {
			fmt.Fprintln(os.Stderr, "（没有对话，未创建会话）")
		} else {
			fmt.Fprintf(os.Stderr, "会话已保存: %s（xaihi show %s）\n", c.ID, c.ID)
		}
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n用法:\n"+
			"  xaihi serve                       起 HTTP 服务\n"+
			"  xaihi chat                        交互式对话\n"+
			"  xaihi send <文本>                  发一条就退出\n"+
			"  xaihi convs | archived            列出未归档 / 已归档会话\n"+
			"  xaihi show <id>                   看一段会话\n"+
			"  xaihi archive|restore <id>        归档 / 恢复\n"+
			"  xaihi rm <id> [--hard]            归档；加 --hard 才是真删\n"+
			"  xaihi config | version            看配置 / 版本\n", cmd)
		os.Exit(2)
	}
	_ = llm.Chunk{}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "✗", err)
	os.Exit(1)
}

// oneLine 把 prompt 压成一行给 list 用
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 40 {
		s = string([]rune(s)[:40]) + "…"
	}
	return s
}
