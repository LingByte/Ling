package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"
)

type AlibabaHandler struct {
	ctx               context.Context
	apiKey            string
	appID             string
	endpoint          string
	systemPrompt      string
	client            *http.Client
	mutex             sync.Mutex
	messages          []openai.ChatCompletionMessage
	maxMemoryMessages int
	interruptCh       chan struct{}
}

func NewAlibabaHandler(ctx context.Context, llmOptions *LLMOptions) (*AlibabaHandler, error) {
	opts := copyOptions(llmOptions)
	timeout := 30 * time.Second
	if s := strings.TrimSpace(os.Getenv("ALIBABA_AI_TIMEOUT_SECONDS")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}
	endpoint := strings.TrimSpace(opts.BaseURL)
	if endpoint == "" {
		endpoint = "https://dashscope.aliyuncs.com"
	}
	appID := strings.TrimSpace(opts.BaseURL)
	if strings.Contains(endpoint, "://") {
		appID = strings.TrimSpace(os.Getenv("ALIBABA_APP_ID"))
	}
	if appID == "" {
		appID = strings.TrimSpace(os.Getenv("ALIBABA_APP_ID"))
	}
	if appID == "" {
		return nil, errors.New("alibaba app id is required (set LLM BaseURL as app id or ALIBABA_APP_ID)")
	}
	return &AlibabaHandler{
		ctx:               ctx,
		apiKey:            strings.TrimSpace(opts.ApiKey),
		appID:             appID,
		endpoint:          endpoint,
		systemPrompt:      opts.SystemPrompt,
		client:            &http.Client{Timeout: timeout},
		messages:          make([]openai.ChatCompletionMessage, 0),
		maxMemoryMessages: defaultMaxMemoryMessages,
		interruptCh:       make(chan struct{}, 1),
	}, nil
}

func (h *AlibabaHandler) Query(text, model string) (string, error) {
	resp, err := h.QueryWithOptions(text, &QueryOptions{Model: model})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty response")
	}
	return resp.Choices[0].Content, nil
}

func (h *AlibabaHandler) QueryWithOptions(text string, options *QueryOptions) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	select {
	case <-h.interruptCh:
		return nil, errors.New("interrupted")
	default:
	}
	h.appendMessage(openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: text})
	reqBody := map[string]any{
		"input": map[string]string{
			"prompt": h.composePrompt(text),
		},
		"parameters": map[string]any{},
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/api/v1/apps/%s/completion", strings.TrimRight(h.endpoint, "/"), h.appID)
	req, err := http.NewRequestWithContext(h.ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("alibaba request failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Output struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	answer := strings.TrimSpace(parsed.Output.Text)
	h.appendMessage(openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: answer})
	return &QueryResponse{
		Provider: h.Provider(),
		Model:    options.Model,
		Choices:  []QueryChoice{{Index: 0, Content: answer, FinishReason: "stop"}},
	}, nil
}

func (h *AlibabaHandler) QueryStream(text string, options *QueryOptions, callback func(segment string, isComplete bool) error) (*QueryResponse, error) {
	resp, err := h.QueryWithOptions(text, options)
	if err != nil {
		return nil, err
	}
	if callback != nil && len(resp.Choices) > 0 {
		if err := callback(resp.Choices[0].Content, false); err != nil {
			return nil, err
		}
		if err := callback("", true); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func (h *AlibabaHandler) Provider() string { return LLM_ALIBABA }

func (h *AlibabaHandler) Interrupt() {
	select {
	case h.interruptCh <- struct{}{}:
	default:
	}
}

func (h *AlibabaHandler) ResetMemory() {
	h.mutex.Lock()
	h.messages = h.messages[:0]
	h.mutex.Unlock()
}

func (h *AlibabaHandler) SummarizeMemory(model string) (string, error) {
	_ = model
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if len(h.messages) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, m := range h.messages {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func (h *AlibabaHandler) SetMaxMemoryMessages(n int) {
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

func (h *AlibabaHandler) GetMaxMemoryMessages() int {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return h.maxMemoryMessages
}

func (h *AlibabaHandler) appendMessage(m openai.ChatCompletionMessage) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.messages = append(h.messages, m)
	if len(h.messages) > h.maxMemoryMessages {
		h.messages = h.messages[len(h.messages)-h.maxMemoryMessages:]
	}
}

func (h *AlibabaHandler) composePrompt(userText string) string {
	if strings.TrimSpace(h.systemPrompt) == "" {
		return strings.TrimSpace(userText)
	}
	return h.systemPrompt + "\n\n用户输入：" + strings.TrimSpace(userText)
}

