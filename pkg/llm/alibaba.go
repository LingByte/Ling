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
	"time"

	"go.uber.org/zap"
)

type AlibabaHandler struct {
	ctx          context.Context
	apiKey       string
	appID        string
	endpoint     string
	systemPrompt string
	client       *http.Client
	mem          *asyncTurnMemory
	interruptCh  chan struct{}
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
	log := zap.NewNop()
	if llmOptions != nil && llmOptions.Logger != nil {
		log = llmOptions.Logger
	}
	return &AlibabaHandler{
		ctx:          ctx,
		apiKey:       strings.TrimSpace(opts.ApiKey),
		appID:        appID,
		endpoint:     endpoint,
		systemPrompt: opts.SystemPrompt,
		client:       &http.Client{Timeout: timeout},
		mem:          newAsyncTurnMemory(ctx, log),
		interruptCh:  make(chan struct{}, 1),
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
	model := strings.TrimSpace(options.Model)
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
	h.mem.appendPairAndMaybeSummarize(h.ctx, model, text, answer, h.summarizeAlibaba)
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
	if h.mem != nil {
		h.mem.reset()
	}
}

func (h *AlibabaHandler) SummarizeMemory(model string) (string, error) {
	if h.mem == nil {
		return "", nil
	}
	return h.mem.summarizeMemorySync(h.ctx, strings.TrimSpace(model), h.summarizeAlibaba)
}

func (h *AlibabaHandler) SetMaxMemoryMessages(n int) {
	if h.mem != nil {
		h.mem.setMaxMemoryMessages(n)
	}
}

func (h *AlibabaHandler) GetMaxMemoryMessages() int {
	if h.mem == nil {
		return defaultMaxMemoryMessages
	}
	return h.mem.getMaxMemoryMessages()
}

func (h *AlibabaHandler) composePrompt(currentUser string) string {
	currentUser = strings.TrimSpace(currentUser)
	var b strings.Builder
	if s := strings.TrimSpace(h.systemPrompt); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	if sum := h.mem.summaryText(); sum != "" {
		b.WriteString("Conversation summary so far: ")
		b.WriteString(sum)
		b.WriteString("\n\n")
	}
	for _, t := range h.mem.snapshotTurns() {
		b.WriteString(t.Role)
		b.WriteString(": ")
		b.WriteString(t.Content)
		b.WriteString("\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString("用户输入：")
	b.WriteString(currentUser)
	out := strings.TrimSpace(b.String())
	if out == "" {
		return currentUser
	}
	return out
}

func (h *AlibabaHandler) summarizeAlibaba(ctx context.Context, model string, transcript string, previousSummary string) (string, error) {
	_ = model
	system := "You are a conversation summarizer. Produce a concise, factual summary of the conversation so far. Preserve user preferences, facts, decisions, and open TODOs. Do not include any markdown."
	user := ""
	if strings.TrimSpace(previousSummary) != "" {
		user += "Existing summary:\n" + previousSummary + "\n\n"
	}
	user += "Conversation transcript:\n" + transcript + "\n\nReturn an updated summary in plain text."
	prompt := system + "\n\n" + user
	reqBody := map[string]any{
		"input": map[string]string{
			"prompt": prompt,
		},
		"parameters": map[string]any{},
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/api/v1/apps/%s/completion", strings.TrimRight(h.endpoint, "/"), h.appID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("alibaba summarize failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Output struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	return strings.TrimSpace(parsed.Output.Text), nil
}

