// Package llm：OpenAI 兼容客户端，只实现本机用得到的部分（流式 + 非流式）。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"
)

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ChatRequest struct {
	Model           string    `json:"model"`
	Messages        []Message `json:"messages"`
	MaxTokens       int       `json:"max_tokens,omitempty"`
	Temperature     *float64  `json:"temperature,omitempty"`
	Stream          bool      `json:"stream"`
	Tools           any       `json:"tools,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	// Strata 扩展：本轮思考的 token 预算（0=引擎默认，即不设上限）
	ReasoningBudget int `json:"reasoning_budget_tokens,omitempty"`
}

type Chunk struct {
	Delta     string // 正文增量
	Reasoning string // 思考增量（Strata 的 reasoning_content / 9B 若开思考也有）
	Done      bool
}

// Stats：上游给的用量与速度（OpenAI usage / llama.cpp timings，有就用）
type Stats struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	PredictedPerSec  float64
}

// UserAgent：标识自己（opencode 之类的网关明确要求"别用通用 SDK 名"）。
// main 启动时设成 xaihi/<版本>；测试里保持 dev。
var UserAgent = "xaihi/dev"

// OnRequest：每发出一次请求回调一次（body 字节数）。
// 用来发现"请求体异常大"——走代理/境外接口时，大 body 是连接被掐的常见原因。
var OnRequest func(bodyBytes int)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// SessionHeader / SessionID：有些网关（比如 OpenCode Go）要求每个对话
	// 带一个稳定的会话 id 做路由与缓存（头名如 x-opencode-session）。
	// SessionHeader 为空就不发 —— 普通上游不受影响。
	SessionHeader string
	SessionID     string
	// Headers：额外固定头（由 profile 配），内置头优先，覆盖不了
	Headers map[string]string

	mu    sync.Mutex
	stats Stats
}

// Stats 返回最近一次流式请求的用量/速度
func (c *Client) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

func (c *Client) setStats(st Stats) {
	c.mu.Lock()
	// 累加/覆盖：以最后一次上游上报为准
	if st.PromptTokens > 0 {
		c.stats.PromptTokens = st.PromptTokens
	}
	if st.CachedTokens > 0 {
		c.stats.CachedTokens = st.CachedTokens
	}
	if st.CompletionTokens > 0 {
		c.stats.CompletionTokens = st.CompletionTokens
	}
	if st.PredictedPerSec > 0 {
		c.stats.PredictedPerSec = st.PredictedPerSec
	}
	c.mu.Unlock()
}

func New(baseURL, apiKey string) *Client {
	return &Client{BaseURL: baseURL, APIKey: apiKey, HTTP: &http.Client{Timeout: 0,
		// Proxy 必须显式给：零值 Transport 的 Proxy 是 nil，等于「永不代理」。
		// 走境外接口（opencode 之类）时直连会走 IPv6，经常被 reset by peer。
		Transport: &http.Transport{
			Proxy:              http.ProxyFromEnvironment,
			DisableKeepAlives:  true,
			DisableCompression: true,
		}}}
}

// post 带重试：**只重试"还没开始读流"的失败**。
//
// 走代理/境外接口时，`read: connection reset by peer` 和裸 `EOF` 很常见 ——
// 连接建好了，但等响应头的时候对端把它掐了。这种失败重试是安全的：
// 请求体还没被消费成一次推理，重发不会产生两次副作用。
// 一旦开始读流就不再重试（那可能已经吐了一半内容，重来会让消息重复）。
func (c *Client) post(ctx context.Context, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if OnRequest != nil {
		OnRequest(len(b))
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 800 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Close = true // 每个请求独立连接：本机 python 代理不支持长连接复用
		// 自定义头先设，内置的（Content-Type/UA/会话/授权）后设 —— 用户配的头
		// 不能覆盖这几个，否则一个手滑就把认证弄坏了
		for k, v := range c.Headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", UserAgent)
		if c.SessionHeader != "" && c.SessionID != "" {
			req.Header.Set(c.SessionHeader, c.SessionID)
		}
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
		resp, err := c.HTTP.Do(req)
		if err == nil {
			// 5xx 也算"还没开始读流"，交给上层判断前先重试
			if resp.StatusCode >= 500 && resp.StatusCode <= 504 && attempt < 2 {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
				resp.Body.Close()
				lastErr = fmt.Errorf("upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
				continue
			}
			return resp, nil
		}
		lastErr = err
		if !retryableNetErr(err) || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// retryableNetErr：只认"连接层面被掐断"这一类，业务错误（4xx）不重试。
func retryableNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset") || strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "broken pipe")
}

// usageLike / timingsLike：兼容两种上游字段
type usageLike struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type timingsLike struct {
	PredictedN      int     `json:"predicted_n"`
	PredictedPerSec float64 `json:"predicted_per_second"`
	PromptN         int     `json:"prompt_n"`
}

func (c *Client) captureStats(u *usageLike, t *timingsLike) {
	st := Stats{}
	if u != nil {
		st.PromptTokens = u.PromptTokens
		st.CompletionTokens = u.CompletionTokens
		if u.PromptTokensDetails != nil {
			st.CachedTokens = u.PromptTokensDetails.CachedTokens
		}
	}
	if t != nil && t.PredictedPerSec > 0 {
		st.PredictedPerSec = t.PredictedPerSec
		if st.CompletionTokens == 0 {
			st.CompletionTokens = t.PredictedN
		}
	}
	if st.PromptTokens > 0 || st.CompletionTokens > 0 || st.PredictedPerSec > 0 {
		c.setStats(st)
	}
}

// Stream 逐块返回增量；onChunk 返回错误即中止。
func (c *Client) Stream(ctx context.Context, req ChatRequest, onChunk func(Chunk) error) error {
	req.Stream = true
	resp, err := c.post(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return fmt.Errorf("upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return onChunk(Chunk{Done: true})
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage   *usageLike   `json:"usage"`
			Timings *timingsLike `json:"timings"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue // 容忍非标准块
		}
		c.captureStats(ev.Usage, ev.Timings)
		for _, ch := range ev.Choices {
			d := ch.Delta
			reasoning := d.ReasoningContent
			if reasoning == "" {
				reasoning = d.Reasoning
			}
			if d.Content != "" || reasoning != "" {
				if err := onChunk(Chunk{Delta: d.Content, Reasoning: reasoning}); err != nil {
					return err
				}
			}
		}
	}
	return sc.Err()
}

// Chat 非流式（P0 的 REPL 也用流式，这个留给测试和工具阶段）。
func (c *Client) Chat(ctx context.Context, req ChatRequest) (string, error) {
	req.Stream = false
	resp, err := c.post(ctx, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return "", fmt.Errorf("upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}
	return out.Choices[0].Message.Content, nil
}

var _ = time.Second

// StreamWithTools：带工具的流式请求；tool_calls 按 index 累积分片后返回。
func (c *Client) StreamWithTools(ctx context.Context, req ChatRequest, onChunk func(Chunk) error, calls *[]ToolCall) error {
	req.Stream = true
	resp, err := c.post(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return fmt.Errorf("upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	acc := map[int]*ToolCall{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ev struct {
			Usage   *usageLike   `json:"usage"`
			Timings *timingsLike `json:"timings"`
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		c.captureStats(ev.Usage, ev.Timings)
		for _, ch := range ev.Choices {
			d := ch.Delta
			r := d.ReasoningContent
			if r == "" {
				r = d.Reasoning
			}
			if d.Content != "" || r != "" {
				if onChunk != nil {
					if err := onChunk(Chunk{Delta: d.Content, Reasoning: r}); err != nil {
						return err
					}
				}
			}
			for _, tc := range d.ToolCalls {
				k := tc.Index
				cur, ok := acc[k]
				if !ok {
					cur = &ToolCall{Index: k}
					acc[k] = cur
				}
				if tc.ID != "" {
					cur.ID = tc.ID
				}
				cur.Type = "function"
				if tc.Function.Name != "" {
					cur.Function.Name += tc.Function.Name
				}
				cur.Function.Arguments += tc.Function.Arguments
			}
		}
	}
	for i := 0; i < len(acc); i++ {
		if c, ok := acc[i]; ok {
			*calls = append(*calls, *c)
		}
	}
	return sc.Err()
}
