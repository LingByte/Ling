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
	"time"

	"go.uber.org/zap"
)

type LMStudioHandler struct {
	ctx          context.Context
	baseURL      string
	apiKey       string
	systemPrompt string
	mem          *asyncTurnMemory
	interruptCh  chan struct{}
	client       *http.Client
}

func NewLMStudioHandler(ctx context.Context, llmOptions *LLMOptions) (*LMStudioHandler, error) {
	opts := copyOptions(llmOptions)
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = "http://localhost:1234/v1"
	}
	if strings.TrimSpace(opts.ApiKey) == "" {
		opts.ApiKey = "lmstudio"
	}
	log := zap.NewNop()
	if llmOptions != nil && llmOptions.Logger != nil {
		log = llmOptions.Logger
	}
	return &LMStudioHandler{
		ctx:          ctx,
		baseURL:      strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/"),
		apiKey:       strings.TrimSpace(opts.ApiKey),
		systemPrompt: opts.SystemPrompt,
		mem:          newAsyncTurnMemory(ctx, log),
		interruptCh:  make(chan struct{}, 1),
		client:       &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (h *LMStudioHandler) Query(text, model string) (string, error) {
	resp, err := h.QueryWithOptions(text, &QueryOptions{Model: model})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty response")
	}
	return resp.Choices[0].Content, nil
}

func (h *LMStudioHandler) QueryWithOptions(text string, options *QueryOptions) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "local-model"
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
	body := map[string]any{
		"model":       model,
		"messages":    h.mem.buildChatCompletionMessages(appendEmotionalStyle(h.systemPrompt, options), text),
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
	h.mem.appendPairAndMaybeSummarize(reqCtx, model, text, content, h.summarizeConversation)
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

func (h *LMStudioHandler) QueryStream(text string, options *QueryOptions, callback func(segment string, isComplete bool) error) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "local-model"
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
	body := map[string]any{
		"model":       model,
		"messages":    h.mem.buildChatCompletionMessages(appendEmotionalStyle(h.systemPrompt, options), text),
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
		return nil, fmt.Errorf("lmstudio stream failed: status=%d body=%s", resp.StatusCode, string(rb))
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
		if json.Unmarshal([]byte(payload), &chunk) != nil || len(chunk.Choices) == 0 {
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
	h.mem.appendPairAndMaybeSummarize(reqCtx, model, text, content, h.summarizeConversation)
	return &QueryResponse{
		Provider: h.Provider(),
		Model:    model,
		Choices:  []QueryChoice{{Index: 0, Content: content, FinishReason: "stop"}},
	}, nil
}

func (h *LMStudioHandler) Provider() string { return LLM_LMSTUDIO }

func (h *LMStudioHandler) Interrupt() {
	select {
	case h.interruptCh <- struct{}{}:
	default:
	}
}

func (h *LMStudioHandler) ResetMemory() {
	if h.mem != nil {
		h.mem.reset()
	}
}

func (h *LMStudioHandler) SummarizeMemory(model string) (string, error) {
	if h.mem == nil {
		return "", nil
	}
	if strings.TrimSpace(model) == "" {
		model = "local-model"
	}
	return h.mem.summarizeMemorySync(h.ctx, model, h.summarizeConversation)
}

func (h *LMStudioHandler) SetMaxMemoryMessages(n int) {
	if h.mem != nil {
		h.mem.setMaxMemoryMessages(n)
	}
}

func (h *LMStudioHandler) GetMaxMemoryMessages() int {
	if h.mem == nil {
		return defaultMaxMemoryMessages
	}
	return h.mem.getMaxMemoryMessages()
}

func (h *LMStudioHandler) summarizeConversation(ctx context.Context, model string, transcript string, previousSummary string) (string, error) {
	system := "You are a conversation summarizer. Produce a concise, factual summary of the conversation so far. Preserve user preferences, facts, decisions, and open TODOs. Do not include any markdown."
	user := ""
	if strings.TrimSpace(previousSummary) != "" {
		user += "Existing summary:\n" + previousSummary + "\n\n"
	}
	user += "Conversation transcript:\n" + transcript + "\n\nReturn an updated summary in plain text."
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream":      false,
		"temperature": 0,
	}
	raw, err := h.doChatCompletion(ctx, body)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", nil
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}


func (h *LMStudioHandler) doChatCompletion(ctx context.Context, body map[string]any) ([]byte, error) {
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
		return nil, fmt.Errorf("lmstudio request failed: status=%d body=%s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

