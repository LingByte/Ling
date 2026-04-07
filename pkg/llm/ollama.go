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

type OllamaHandler struct {
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

func NewOllamaHandler(ctx context.Context, llmOptions *LLMOptions) (*OllamaHandler, error) {
	opts := copyOptions(llmOptions)
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = "http://localhost:11434/v1"
	}
	if strings.TrimSpace(opts.ApiKey) == "" {
		opts.ApiKey = "ollama"
	}
	return &OllamaHandler{
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

func (h *OllamaHandler) Query(text, model string) (string, error) {
	resp, err := h.QueryWithOptions(text, &QueryOptions{Model: model})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty response")
	}
	return resp.Choices[0].Content, nil
}

func (h *OllamaHandler) QueryWithOptions(text string, options *QueryOptions) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "llama3.1"
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

	msgs := h.buildMessages(text)
	body := map[string]any{
		"model":       model,
		"messages":    msgs,
		"stream":      false,
		"temperature": options.Temperature,
	}
	raw, err := h.doChatCompletion(reqCtx, body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	content := ""
	reason := "stop"
	if len(parsed.Choices) > 0 {
		content = strings.TrimSpace(parsed.Choices[0].Message.Content)
		if parsed.Choices[0].FinishReason != "" {
			reason = parsed.Choices[0].FinishReason
		}
	}
	h.appendTurn(text, content)
	return &QueryResponse{
		Provider: h.Provider(),
		Model:    model,
		Choices:  []QueryChoice{{Index: 0, Content: content, FinishReason: reason}},
		Usage: &TokenUsage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		},
	}, nil
}

func (h *OllamaHandler) QueryStream(text string, options *QueryOptions, callback func(segment string, isComplete bool) error) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "llama3.1"
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

	msgs := h.buildMessages(text)
	body := map[string]any{
		"model":       model,
		"messages":    msgs,
		"stream":      true,
		"temperature": options.Temperature,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, h.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama stream failed: status=%d body=%s", resp.StatusCode, string(rb))
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
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		seg := chunk.Choices[0].Delta.Content
		if seg == "" {
			continue
		}
		out.WriteString(seg)
		if callback != nil {
			if err := callback(seg, false); err != nil {
				return nil, err
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
	content := strings.TrimSpace(out.String())
	h.appendTurn(text, content)
	return &QueryResponse{
		Provider: h.Provider(),
		Model:    model,
		Choices:  []QueryChoice{{Index: 0, Content: content, FinishReason: "stop"}},
	}, nil
}

func (h *OllamaHandler) Provider() string { return LLM_OLLAMA }

func (h *OllamaHandler) Interrupt() {
	select {
	case h.interruptCh <- struct{}{}:
	default:
	}
}

func (h *OllamaHandler) ResetMemory() {
	h.mutex.Lock()
	h.messages = h.messages[:0]
	h.mutex.Unlock()
}

func (h *OllamaHandler) SummarizeMemory(model string) (string, error) {
	_ = model
	h.mutex.Lock()
	defer h.mutex.Unlock()
	var b strings.Builder
	for _, m := range h.messages {
		b.WriteString(m.Role + ": " + m.Content + "\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func (h *OllamaHandler) SetMaxMemoryMessages(n int) {
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

func (h *OllamaHandler) GetMaxMemoryMessages() int {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return h.maxMemoryMessages
}

func (h *OllamaHandler) buildMessages(userText string) []map[string]string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	msgs := make([]map[string]string, 0, len(h.messages)+2)
	if strings.TrimSpace(h.systemPrompt) != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": h.systemPrompt})
	}
	for _, m := range h.messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": userText})
	return msgs
}

func (h *OllamaHandler) appendTurn(userText, assistantText string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.messages = append(h.messages, llmMemoryMessage{Role: "user", Content: userText}, llmMemoryMessage{Role: "assistant", Content: assistantText})
	if len(h.messages) > h.maxMemoryMessages {
		h.messages = h.messages[len(h.messages)-h.maxMemoryMessages:]
	}
}


func (h *OllamaHandler) doChatCompletion(ctx context.Context, body map[string]any) ([]byte, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama request failed: status=%d body=%s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

