package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// 常驻插件通道（JSON-RPC 2.0 over stdio，行分隔）
//
//	→ {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1"}}
//	← {"jsonrpc":"2.0","id":1,"result":{"ok":true}}
//	→ {"jsonrpc":"2.0","id":2,"method":"tools/list"}
//	← {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name","description","params","required"}]}}
//	→ {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"x","arguments":{…}}}
//	← {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"…"},{"type":"image","url":"data:…"}]}}
//
// 一个常驻进程可以暴露多个工具（与 MCP 同构）；进程挂了会在下次调用时自动重启。

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type persistentProc struct {
	path    string
	argv    []string
	env     map[string]string
	timeout time.Duration

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  *os.File
	nextID int
	wait   map[int]chan rpcResp
	dead   bool
}

var (
	pmu   sync.Mutex
	procs = map[string]*persistentProc{}
)

// ShutdownPlugins：退出前关掉所有常驻插件进程
func ShutdownPlugins() {
	pmu.Lock()
	defer pmu.Unlock()
	for _, p := range procs {
		p.close()
	}
	procs = map[string]*persistentProc{}
}

func (p *persistentProc) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dead = true
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// ensureProc 返回可用的常驻进程（必要时启动 + initialize + tools/list）
func ensureProc(m manifest, path string) (*persistentProc, []manifestTool, error) {
	pmu.Lock()
	defer pmu.Unlock()
	if p, ok := procs[path]; ok && !p.dead {
		tools, err := p.listTools()
		if err == nil {
			return p, tools, nil
		}
		p.close()
		delete(procs, path)
	}
	p := &persistentProc{path: path, env: m.Env, timeout: time.Duration(m.TimeoutMs) * time.Millisecond,
		wait: map[int]chan rpcResp{}}
	if p.timeout <= 0 {
		p.timeout = 30 * time.Second
	}
	if err := p.start(m.Command); err != nil {
		return nil, nil, err
	}
	if _, err := p.call("initialize", map[string]any{"protocolVersion": "1", "name": m.Name}); err != nil {
		p.close()
		return nil, nil, fmt.Errorf("initialize: %w", err)
	}
	tools, err := p.listTools()
	if err != nil {
		p.close()
		return nil, nil, fmt.Errorf("tools/list: %w", err)
	}
	if len(tools) == 0 {
		p.close()
		return nil, nil, fmt.Errorf("tools/list 为空")
	}
	procs[path] = p
	return p, tools, nil
}

func (p *persistentProc) start(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("command 为空")
	}
	argv = append([]string{}, argv...)
	argv[0] = expandHome(argv[0])
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "XAIHI_PLUGIN=1")
	for k, v := range p.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd, p.stdin, p.dead = cmd, stdin.(*os.File), false
	go p.readLoop(stdout)
	return nil
}

func (p *persistentProc) readLoop(r interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var resp rpcResp
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		p.mu.Lock()
		ch, ok := p.wait[resp.ID]
		delete(p.wait, resp.ID)
		if !ok {
			p.mu.Unlock()
			continue
		}
		p.mu.Unlock()
		select {
		case ch <- resp:
		default:
		}
	}
	p.mu.Lock()
	p.dead = true
	for id, ch := range p.wait {
		close(ch)
		delete(p.wait, id)
	}
	p.mu.Unlock()
}

func (p *persistentProc) call(method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	if p.dead || p.stdin == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("插件进程已退出")
	}
	p.nextID++
	id := p.nextID
	ch := make(chan rpcResp, 1)
	p.wait[id] = ch
	p.mu.Unlock()

	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := p.stdin.Write(append(msg, '\n')); err != nil {
		return nil, err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("插件进程断开")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("%s", resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(p.timeout):
		return nil, fmt.Errorf("超时（%s 未响应）", p.timeout)
	}
}

type manifestTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Params      map[string]any `json:"params"`
	Required    []string       `json:"required"`
}

func (p *persistentProc) listTools() ([]manifestTool, error) {
	raw, err := p.call("tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []manifestTool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// callTool：执行一次工具调用，返回文本或 text+image 内容数组
func (p *persistentProc) callTool(ctx context.Context, name string, args map[string]any) (any, error) {
	type res struct {
		raw json.RawMessage
		err error
	}
	done := make(chan res, 1)
	go func() {
		raw, err := p.call("tools/call", map[string]any{"name": name, "arguments": args})
		done <- res{raw, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return "✗ " + r.err.Error(), nil
		}
		return parsePersistentResult(r.raw), nil
	case <-ctx.Done():
		return "✗ 已取消", nil
	}
}

func parsePersistentResult(raw json.RawMessage) any {
	var out struct {
		Text    string           `json:"text"`
		Images  []string         `json:"images"`
		Content []map[string]any `json:"content"`
	}
	_ = json.Unmarshal(raw, &out)
	if len(out.Content) > 0 {
		parts := make([]map[string]any, 0, len(out.Content))
		for _, c := range out.Content {
			switch c["type"] {
			case "text":
				if s, ok := c["text"].(string); ok && s != "" {
					parts = append(parts, map[string]any{"type": "text", "text": s})
				}
			case "image":
				u, _ := c["url"].(string)
				if u == "" {
					u, _ = c["data"].(string)
				}
				if u != "" {
					parts = append(parts, map[string]any{"type": "image", "url": u})
				}
			}
		}
		if len(parts) == 1 {
			if s, ok := parts[0]["text"].(string); ok {
				return s
			}
		}
		if len(parts) > 0 {
			return parts
		}
	}
	if out.Text != "" && len(out.Images) == 0 {
		return out.Text
	}
	if out.Text != "" || len(out.Images) > 0 {
		parts := []map[string]any{{"type": "text", "text": out.Text}}
		for _, img := range out.Images {
			parts = append(parts, map[string]any{"type": "image", "url": img})
		}
		return parts
	}
	return ""
}
