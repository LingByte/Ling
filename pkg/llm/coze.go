package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/coze-dev/coze-go"
)

type CozeHandler struct {
	client            coze.CozeAPI
	ctx               context.Context
	botID             string
	userID            string
	systemPrompt      string
	mutex             sync.Mutex
	messages          []coze.Message
	maxMemoryMessages int
	interruptCh       chan struct{}
}

func NewCozeHandler(ctx context.Context, llmOptions *LLMOptions) (*CozeHandler, error) {
	opts := copyOptions(llmOptions)
	cfg := struct {
		BotID   string `json:"botId"`
		UserID  string `json:"userId"`
		BaseURL string `json:"baseUrl"`
	}{}
	if raw := strings.TrimSpace(opts.BaseURL); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
		if cfg.BotID == "" && !strings.Contains(raw, "{") {
			cfg.BotID = raw
		}
	}
	if cfg.BotID == "" {
		return nil, errors.New("coze botId is required (set LLM BaseURL as JSON {botId,userId,baseUrl} or plain botId)")
	}
	if cfg.UserID == "" {
		cfg.UserID = "default_user"
	}
	authClient := coze.NewTokenAuth(strings.TrimSpace(opts.ApiKey))
	client := coze.NewCozeAPI(authClient)
	if strings.TrimSpace(cfg.BaseURL) != "" {
		client = coze.NewCozeAPI(authClient, coze.WithBaseURL(strings.TrimSpace(cfg.BaseURL)))
	}
	return &CozeHandler{
		client:            client,
		ctx:               ctx,
		botID:             cfg.BotID,
		userID:            cfg.UserID,
		systemPrompt:      opts.SystemPrompt,
		messages:          make([]coze.Message, 0),
		maxMemoryMessages: defaultMaxMemoryMessages,
		interruptCh:       make(chan struct{}, 1),
	}, nil
}

func (h *CozeHandler) Query(text, model string) (string, error) {
	resp, err := h.QueryWithOptions(text, &QueryOptions{Model: model})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty response")
	}
	return resp.Choices[0].Content, nil
}

func (h *CozeHandler) QueryWithOptions(text string, options *QueryOptions) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	h.appendMessage(coze.Message{Role: "user", Content: text})
	msgs := h.snapshotMessages()
	streamFlag := false
	req := &coze.CreateChatsReq{
		BotID:    h.botID,
		UserID:   h.userID,
		Messages: toCozePtrs(msgs),
		Stream:   &streamFlag,
	}
	ctx, cancel := context.WithTimeout(h.ctx, 60*time.Second)
	defer cancel()
	stream, err := h.client.Chat.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var out strings.Builder
	for {
		select {
		case <-h.interruptCh:
			return nil, errors.New("interrupted")
		default:
		}
		ev, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if ev.Message != nil && ev.Message.Content != "" {
			out.WriteString(ev.Message.Content)
		}
		if ev.IsDone() || ev.Event == coze.ChatEventConversationMessageCompleted {
			break
		}
	}
	answer := strings.TrimSpace(out.String())
	h.appendMessage(coze.Message{Role: "assistant", Content: answer})
	return &QueryResponse{
		Provider: h.Provider(),
		Model:    options.Model,
		Choices:  []QueryChoice{{Index: 0, Content: answer, FinishReason: "stop"}},
	}, nil
}

func (h *CozeHandler) QueryStream(text string, options *QueryOptions, callback func(segment string, isComplete bool) error) (*QueryResponse, error) {
	if options == nil {
		options = &QueryOptions{}
	}
	h.appendMessage(coze.Message{Role: "user", Content: text})
	msgs := h.snapshotMessages()
	streamFlag := true
	req := &coze.CreateChatsReq{
		BotID:    h.botID,
		UserID:   h.userID,
		Messages: toCozePtrs(msgs),
		Stream:   &streamFlag,
	}
	ctx, cancel := context.WithTimeout(h.ctx, 60*time.Second)
	defer cancel()
	stream, err := h.client.Chat.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var out strings.Builder
	for {
		select {
		case <-h.interruptCh:
			return nil, errors.New("interrupted")
		default:
		}
		ev, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if ev.Message != nil && ev.Message.Content != "" {
			seg := ev.Message.Content
			out.WriteString(seg)
			if callback != nil {
				if err := callback(seg, false); err != nil {
					return nil, err
				}
			}
		}
		if ev.IsDone() || ev.Event == coze.ChatEventConversationMessageCompleted {
			break
		}
	}
	if callback != nil {
		if err := callback("", true); err != nil {
			return nil, err
		}
	}
	answer := strings.TrimSpace(out.String())
	h.appendMessage(coze.Message{Role: "assistant", Content: answer})
	return &QueryResponse{
		Provider: h.Provider(),
		Model:    options.Model,
		Choices:  []QueryChoice{{Index: 0, Content: answer, FinishReason: "stop"}},
	}, nil
}

func (h *CozeHandler) Provider() string { return LLM_COZE }

func (h *CozeHandler) Interrupt() {
	select {
	case h.interruptCh <- struct{}{}:
	default:
	}
}

func (h *CozeHandler) ResetMemory() {
	h.mutex.Lock()
	h.messages = h.messages[:0]
	h.mutex.Unlock()
}

func (h *CozeHandler) SummarizeMemory(model string) (string, error) {
	_ = model
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if len(h.messages) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, m := range h.messages {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func (h *CozeHandler) SetMaxMemoryMessages(n int) {
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

func (h *CozeHandler) GetMaxMemoryMessages() int {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return h.maxMemoryMessages
}

func (h *CozeHandler) appendMessage(m coze.Message) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.messages = append(h.messages, m)
	if len(h.messages) > h.maxMemoryMessages {
		h.messages = h.messages[len(h.messages)-h.maxMemoryMessages:]
	}
}

func (h *CozeHandler) snapshotMessages() []coze.Message {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	out := make([]coze.Message, 0, len(h.messages)+1)
	if strings.TrimSpace(h.systemPrompt) != "" {
		out = append(out, coze.Message{Role: "user", Content: "System: " + h.systemPrompt})
	}
	out = append(out, h.messages...)
	return out
}

func toCozePtrs(in []coze.Message) []*coze.Message {
	out := make([]*coze.Message, 0, len(in))
	for i := range in {
		m := in[i]
		out = append(out, &coze.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

