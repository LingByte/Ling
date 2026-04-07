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
	"time"
)

type AnthropicHandler struct {
	ctx               context.Context
	baseURL           string
	apiKey            string
	systemPrompt      string
	mutex             sync.Mutex
	messages          []llmMemoryMessage
	maxMemoryMessages int
	interruptCh       chan struct{}
	client            *http.Client
}

func NewAnthropicHandler(ctx context.Context, llmOptions *LLMOptions) (*AnthropicHandler, error) {
	opts := copyOptions(llmOptions)
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = "https://api.anthropic.com/v1"
	}
	return &AnthropicHandler{
		ctx:               ctx,
		baseURL:           strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/"),
		apiKey:            strings.TrimSpace(opts.ApiKey),
		systemPrompt:      opts.SystemPrompt,
		messages:          make([]llmMemoryMessage, 0),
		maxMemoryMessages: defaultMaxMemoryMessages,
		interruptCh:       make(chan struct{}, 1),
		client:            &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (h *AnthropicHandler) Query(text, model string) (string, error) {
	resp, err := h.QueryWithOptions(text, &QueryOptions{Model: model})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty response")
	}
	return resp.Choices[0].Content, nil
}

func (h *AnthropicHandler) QueryWithOptions(text string, options *QueryOptions) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}
	reqCtx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	go func() {
		select {
		case <-h.interruptCh:
			cancel()
		case <-reqCtx.Done():
		}
	}()

	userMsgs := h.buildAnthropicMessages(text)
	reqBody := map[string]any{
		"model":       model,
		"max_tokens":  max(256, options.MaxTokens),
		"temperature": options.Temperature,
		"messages":    userMsgs,
	}
	if strings.TrimSpace(h.systemPrompt) != "" {
		reqBody["system"] = h.systemPrompt
	}
	raw, err := h.doAnthropic(reqCtx, reqBody)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	answer := strings.TrimSpace(b.String())
	reason := parsed.StopReason
	if reason == "" {
		reason = "stop"
	}
	h.appendTurn(text, answer)
	return &QueryResponse{
		Provider: h.Provider(),
		Model:    model,
		Choices:  []QueryChoice{{Index: 0, Content: answer, FinishReason: reason}},
		Usage: &TokenUsage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.InputTokens + parsed.Usage.OutputTokens,
		},
	}, nil
}

func (h *AnthropicHandler) QueryStream(text string, options *QueryOptions, callback func(segment string, isComplete bool) error) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}
	reqCtx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	go func() {
		select {
		case <-h.interruptCh:
			cancel()
		case <-reqCtx.Done():
		}
	}()

	reqBody := map[string]any{
		"model":       model,
		"max_tokens":  max(256, options.MaxTokens),
		"temperature": options.Temperature,
		"messages":    h.buildAnthropicMessages(text),
		"stream":      true,
	}
	if strings.TrimSpace(h.systemPrompt) != "" {
		reqBody["system"] = h.systemPrompt
	}
	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, h.baseURL+"/messages", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", h.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic stream failed: status=%d body=%s", resp.StatusCode, string(rb))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var out strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var evt struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(payload), &evt) != nil {
			continue
		}
		if evt.Type == "content_block_delta" && evt.Delta.Type == "text_delta" && evt.Delta.Text != "" {
			out.WriteString(evt.Delta.Text)
			if callback != nil {
				if err := callback(evt.Delta.Text, false); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if callback != nil {
		if err := callback("", true); err != nil {
			return nil, err
		}
	}
	answer := strings.TrimSpace(out.String())
	h.appendTurn(text, answer)
	return &QueryResponse{
		Provider: h.Provider(),
		Model:    model,
		Choices:  []QueryChoice{{Index: 0, Content: answer, FinishReason: "stop"}},
	}, nil
}

func (h *AnthropicHandler) Provider() string { return LLM_ANTHROPIC }

func (h *AnthropicHandler) Interrupt() {
	select {
	case h.interruptCh <- struct{}{}:
	default:
	}
}

func (h *AnthropicHandler) ResetMemory() {
	h.mutex.Lock()
	h.messages = h.messages[:0]
	h.mutex.Unlock()
}

func (h *AnthropicHandler) SummarizeMemory(model string) (string, error) {
	_ = model
	h.mutex.Lock()
	defer h.mutex.Unlock()
	var b strings.Builder
	for _, m := range h.messages {
		b.WriteString(m.Role + ": " + m.Content + "\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func (h *AnthropicHandler) SetMaxMemoryMessages(n int) {
	if n <= 0 {
		n = defaultMaxMemoryMessages
	}
	h.mutex.Lock()
	h.maxMemoryMessages = n
	if len(h.messages) > n {
		h.messages = h.messages[len(h.messages)-n:]
	}
	h.mutex.Unlock()
}

func (h *AnthropicHandler) GetMaxMemoryMessages() int {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return h.maxMemoryMessages
}

func (h *AnthropicHandler) buildAnthropicMessages(userText string) []map[string]any {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	msgs := make([]map[string]any, 0, len(h.messages)+1)
	for _, m := range h.messages {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		msgs = append(msgs, map[string]any{
			"role": role,
			"content": []map[string]string{
				{"type": "text", "text": m.Content},
			},
		})
	}
	msgs = append(msgs, map[string]any{
		"role": "user",
		"content": []map[string]string{
			{"type": "text", "text": userText},
		},
	})
	return msgs
}

func (h *AnthropicHandler) appendTurn(userText, assistantText string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.messages = append(h.messages, llmMemoryMessage{Role: "user", Content: userText}, llmMemoryMessage{Role: "assistant", Content: assistantText})
	if len(h.messages) > h.maxMemoryMessages {
		h.messages = h.messages[len(h.messages)-h.maxMemoryMessages:]
	}
}

func (h *AnthropicHandler) doAnthropic(ctx context.Context, body map[string]any) ([]byte, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/messages", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", h.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("anthropic request failed: status=%d body=%s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

