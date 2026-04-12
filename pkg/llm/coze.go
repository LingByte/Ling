package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/coze-dev/coze-go"
	"go.uber.org/zap"
)

type CozeHandler struct {
	client       coze.CozeAPI
	ctx          context.Context
	botID        string
	userID       string
	systemPrompt string
	mem          *asyncTurnMemory
	interruptCh  chan struct{}
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
	log := zap.NewNop()
	if llmOptions != nil && llmOptions.Logger != nil {
		log = llmOptions.Logger
	}
	return &CozeHandler{
		client:       client,
		ctx:          ctx,
		botID:        cfg.BotID,
		userID:       cfg.UserID,
		systemPrompt: opts.SystemPrompt,
		mem:          newAsyncTurnMemory(ctx, log),
		interruptCh:  make(chan struct{}, 1),
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
	model := strings.TrimSpace(options.Model)
	msgs := h.cozeMessagesForChat(text)
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
	h.mem.appendPairAndMaybeSummarize(ctx, model, text, answer, h.summarizeCoze)
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
	model := strings.TrimSpace(options.Model)
	msgs := h.cozeMessagesForChat(text)
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
	h.mem.appendPairAndMaybeSummarize(ctx, model, text, answer, h.summarizeCoze)
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
	if h.mem != nil {
		h.mem.reset()
	}
}

func (h *CozeHandler) SummarizeMemory(model string) (string, error) {
	if h.mem == nil {
		return "", nil
	}
	return h.mem.summarizeMemorySync(h.ctx, strings.TrimSpace(model), h.summarizeCoze)
}

func (h *CozeHandler) SetMaxMemoryMessages(n int) {
	if h.mem != nil {
		h.mem.setMaxMemoryMessages(n)
	}
}

func (h *CozeHandler) GetMaxMemoryMessages() int {
	if h.mem == nil {
		return defaultMaxMemoryMessages
	}
	return h.mem.getMaxMemoryMessages()
}

func (h *CozeHandler) cozeMessagesForChat(userText string) []coze.Message {
	out := make([]coze.Message, 0, 8)
	if strings.TrimSpace(h.systemPrompt) != "" {
		out = append(out, coze.Message{Role: coze.MessageRoleUser, Content: "System: " + strings.TrimSpace(h.systemPrompt)})
	}
	if sum := h.mem.summaryText(); sum != "" {
		out = append(out, coze.Message{Role: coze.MessageRoleUser, Content: "Conversation summary so far: " + sum})
	}
	for _, t := range h.mem.snapshotTurns() {
		role := coze.MessageRoleUser
		if t.Role == "assistant" {
			role = coze.MessageRoleAssistant
		}
		out = append(out, coze.Message{Role: role, Content: t.Content})
	}
	out = append(out, coze.Message{Role: coze.MessageRoleUser, Content: userText})
	return out
}

func (h *CozeHandler) summarizeCoze(ctx context.Context, model string, transcript string, previousSummary string) (string, error) {
	_ = model
	prompt := "You are a conversation summarizer. Produce a concise, factual summary of the conversation so far. Preserve user preferences, facts, decisions, and open TODOs. Do not include any markdown.\n\n"
	if strings.TrimSpace(previousSummary) != "" {
		prompt += "Existing summary:\n" + previousSummary + "\n\n"
	}
	prompt += "Conversation transcript:\n" + transcript + "\n\nReturn an updated summary in plain text."
	streamFlag := false
	req := &coze.CreateChatsReq{
		BotID:  h.botID,
		UserID: h.userID + "_ling_mem",
		Messages: []*coze.Message{
			{Role: coze.MessageRoleUser, Content: prompt},
		},
		Stream: &streamFlag,
	}
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	stream, err := h.client.Chat.Stream(sctx, req)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	var out strings.Builder
	for {
		ev, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
		if ev.Message != nil && ev.Message.Content != "" {
			out.WriteString(ev.Message.Content)
		}
		if ev.IsDone() || ev.Event == coze.ChatEventConversationMessageCompleted {
			break
		}
	}
	return strings.TrimSpace(out.String()), nil
}

func toCozePtrs(in []coze.Message) []*coze.Message {
	out := make([]*coze.Message, 0, len(in))
	for i := range in {
		m := in[i]
		out = append(out, &coze.Message{Role: m.Role, Content: m.Content})
	}
	return out
}
