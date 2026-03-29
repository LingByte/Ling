package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/LingByte/Ling/pkg/constants"
	"github.com/LingByte/Ling/pkg/utils"
	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

var emojiRegex = regexp.MustCompile(`([\x{00A9}\x{00AE}\x{203C}\x{2049}\x{2122}\x{2139}\x{2194}-\x{2199}\x{21A9}-\x{21AA}\x{231A}-\x{231B}\x{2328}\x{23CF}\x{23E9}-\x{23F3}\x{23F8}-\x{23FA}\x{24C2}\x{25AA}-\x{25AB}\x{25B6}\x{25C0}\x{25FB}-\x{25FE}\x{2600}-\x{26FF}\x{2700}-\x{27BF}\x{2B05}-\x{2B07}\x{2B1B}-\x{2B1C}\x{2B50}\x{2B55}\x{3030}\x{303D}\x{3297}\x{3299}\x{1F004}\x{1F0CF}\x{1F170}-\x{1F251}\x{1F300}-\x{1F5FF}\x{1F600}-\x{1F64F}\x{1F680}-\x{1F6FF}\x{1F910}-\x{1F93E}\x{1F940}-\x{1F94C}\x{1F950}-\x{1F96B}\x{1F980}-\x{1F997}\x{1F9C0}-\x{1F9E6}\x{1FA70}-\x{1FA74}\x{1FA78}-\x{1FA7A}\x{1FA80}-\x{1FA86}\x{1FA90}-\x{1FAA8}\x{1FAB0}-\x{1FAB6}\x{1FAC0}-\x{1FAC2}\x{1FAD0}-\x{1FAD6}\x{1F1E6}-\x{1F1FF}\x{200D}\x{FE0F}])`)

const defaultMaxMemoryMessages = 40

const maxSummarizeInputChars = 12000

func buildTranscriptForSummary(messages []openai.ChatCompletionMessage) string {
	if len(messages) == 0 {
		return ""
	}
	b := strings.Builder{}
	for _, m := range messages {
		role := m.Role
		if role == "" {
			role = "unknown"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
		if b.Len() >= maxSummarizeInputChars {
			break
		}
	}
	s := b.String()
	if len(s) > maxSummarizeInputChars {
		s = s[len(s)-maxSummarizeInputChars:]
	}
	return s
}

type OpenaiHandler struct {
	ctx               context.Context
	client            *openai.Client
	systemPrompt      string
	baseUrl           string
	logger            *zap.Logger
	mutex             sync.Mutex
	messages          []openai.ChatCompletionMessage
	summary           string
	maxMemoryMessages int
	summarizing       atomic.Bool
	summarizeSeq      uint64
}

func NewOpenaiHandler(ctx context.Context, llmOptions *LLMOptions) (*OpenaiHandler, error) {
	if llmOptions == nil {
		return nil, errors.New("options cannot be nil")
	}
	if llmOptions.logger == nil {
		llmOptions.logger = zap.NewNop()
	}
	config := openai.DefaultConfig(llmOptions.ApiKey)
	config.BaseURL = llmOptions.BaseURL
	client := openai.NewClientWithConfig(config)
	return &OpenaiHandler{
		ctx:               ctx,
		client:            client,
		baseUrl:           llmOptions.BaseURL,
		systemPrompt:      llmOptions.SystemPrompt,
		logger:            llmOptions.logger,
		messages:          make([]openai.ChatCompletionMessage, 0),
		maxMemoryMessages: defaultMaxMemoryMessages,
	}, nil
}

func (oh *OpenaiHandler) ResetMemory() {
	oh.mutex.Lock()
	oh.messages = oh.messages[:0]
	oh.summary = ""
	oh.mutex.Unlock()
}

func (oh *OpenaiHandler) SummarizeMemory(model string) (string, error) {
	// Snapshot under lock.
	oh.mutex.Lock()
	if len(oh.messages) == 0 {
		s := oh.summary
		oh.mutex.Unlock()
		return s, nil
	}
	if oh.summarizing.Load() {
		oh.mutex.Unlock()
		return "", errors.New("summarization already in progress")
	}
	oh.summarizing.Store(true)
	seq := atomic.AddUint64(&oh.summarizeSeq, 1)
	previousSummary := oh.summary
	snapshot := make([]openai.ChatCompletionMessage, len(oh.messages))
	copy(snapshot, oh.messages)
	oh.mutex.Unlock()

	newSummary, err := oh.generateSummary(model, snapshot, previousSummary)
	newSummary = strings.TrimSpace(newSummary)

	oh.mutex.Lock()
	defer oh.mutex.Unlock()
	defer oh.summarizing.Store(false)
	if seq != atomic.LoadUint64(&oh.summarizeSeq) {
		// Newer summarization request happened; don't overwrite.
		if err != nil {
			return "", err
		}
		return oh.summary, nil
	}
	if err != nil {
		return "", err
	}
	if newSummary == "" {
		return oh.summary, nil
	}
	oh.summary = newSummary
	oh.messages = oh.messages[:0]
	return oh.summary, nil
}

func (oh *OpenaiHandler) generateSummary(model string, messages []openai.ChatCompletionMessage, previousSummary string) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}
	transcript := buildTranscriptForSummary(messages)
	requestID := GenerateLingRequestID()

	system := "You are a conversation summarizer. Produce a concise, factual summary of the conversation so far. Preserve user preferences, facts, decisions, and open TODOs. Do not include any markdown."
	user := ""
	if previousSummary != "" {
		user += "Existing summary:\n" + previousSummary + "\n\n"
	}
	user += "Conversation transcript:\n" + transcript + "\n\n"
	user += "Return an updated summary in plain text."

	req := openai.ChatCompletionRequest{
		Model: model,
		User:  requestID,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: system},
			{Role: openai.ChatMessageRoleUser, Content: user},
		},
		MaxTokens:   512,
		Temperature: 0,
		TopP:        1,
	}

	resp, err := oh.client.CreateChatCompletion(oh.ctx, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", nil
	}
	newSummary := strings.TrimSpace(resp.Choices[0].Message.Content)
	if newSummary == "" {
		return "", nil
	}
	if emojiRegex != nil {
		newSummary = emojiRegex.ReplaceAllString(newSummary, "")
	}
	return newSummary, nil
}

func (oh *OpenaiHandler) startAsyncSummarizeIfNeeded(model string, snapshot []openai.ChatCompletionMessage, previousSummary string, seq uint64) {
	go func() {
		newSummary, err := oh.generateSummary(model, snapshot, previousSummary)
		oh.mutex.Lock()
		defer oh.mutex.Unlock()
		defer oh.summarizing.Store(false)
		if seq != atomic.LoadUint64(&oh.summarizeSeq) {
			return
		}
		if err != nil {
			oh.logger.Warn("async memory summarization failed", zap.Error(err))
			return
		}
		newSummary = strings.TrimSpace(newSummary)
		if newSummary == "" {
			return
		}
		oh.summary = newSummary
		oh.messages = oh.messages[:0]
	}()
}

func (oh *OpenaiHandler) SetMaxMemoryMessages(n int) {
	if n <= 0 {
		n = defaultMaxMemoryMessages
	}
	oh.mutex.Lock()
	oh.maxMemoryMessages = n
	if len(oh.messages) > oh.maxMemoryMessages {
		oh.messages = oh.messages[len(oh.messages)-oh.maxMemoryMessages:]
	}
	oh.mutex.Unlock()
}

func (oh *OpenaiHandler) GetMaxMemoryMessages() int {
	oh.mutex.Lock()
	v := oh.maxMemoryMessages
	oh.mutex.Unlock()
	return v
}

func (oh *OpenaiHandler) Provider() string {
	return LLM_OPENAI
}

func (oh *OpenaiHandler) Query(text, model string) (string, error) {
	resp, err := oh.QueryWithOptions(text, &QueryOptions{
		Model: model,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty response choices")
	}
	return resp.Choices[0].Content, nil
}

func (oh *OpenaiHandler) QueryWithOptions(text string, options *QueryOptions) (*QueryResponse, error) {
	if options == nil {
		return nil, errors.New("options cannot be nil")
	}
	n := options.N
	if n <= 0 {
		n = 1
	}
	model := options.Model
	if model == "" {
		model = constants.DefaultModel
	}
	estimatedMaxOutputChars := 0
	if options.MaxTokens > 0 {
		estimatedMaxOutputChars = options.MaxTokens * 4
	}

	requestID := GenerateLingRequestID()
	requestedOutputFormat := options.OutputFormat
	if requestedOutputFormat == "" && options.EnableJSONOutput {
		requestedOutputFormat = OutputFormatJSONObject
	}
	requestedOutputFormatLower := strings.ToLower(requestedOutputFormat)
	requiresJSONWrapper := requestedOutputFormatLower == OutputFormatXML ||
		requestedOutputFormatLower == OutputFormatHTML ||
		requestedOutputFormatLower == OutputFormatSQL
	responseFormatApplied := false
	appliedResponseFormat := ""
	var responseFormat *openai.ChatCompletionResponseFormat
	switch requestedOutputFormatLower {
	case "", OutputFormatText:
		// default
	case OutputFormatJSON, OutputFormatJSONObject:
		responseFormatApplied = true
		appliedResponseFormat = OutputFormatJSONObject
		responseFormat = &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
	case OutputFormatJSONSchema:
		// json_schema requires a schema object; since QueryOptions doesn't carry it yet, fallback to json_object.
		responseFormatApplied = true
		appliedResponseFormat = OutputFormatJSONObject
		responseFormat = &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
	default:
		if requiresJSONWrapper {
			responseFormatApplied = true
			appliedResponseFormat = OutputFormatJSONObject
			responseFormat = &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
		}
	}

	formatInstruction := ""
	if requiresJSONWrapper {
		formatInstruction = "Return a JSON object with keys: format, content. format must be \"" + requestedOutputFormatLower + "\". content must be strictly " + requestedOutputFormatLower + " and must not be wrapped in markdown."
	}

	extractStructuredContent := func(raw string) string {
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return raw
		}
		if v, ok := obj["content"]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return raw
	}

	sanitizedMessages := make([]openai.ChatCompletionMessage, 0)
	if oh.systemPrompt != "" {
		sanitizedMessages = append(sanitizedMessages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: oh.systemPrompt,
		})
	}

	var summarySnapshot string
	var historySnapshot []openai.ChatCompletionMessage
	var maxMemoryMessages int
	oh.mutex.Lock()
	summarySnapshot = oh.summary
	maxMemoryMessages = oh.maxMemoryMessages
	if len(oh.messages) > 0 {
		historySnapshot = make([]openai.ChatCompletionMessage, len(oh.messages))
		copy(historySnapshot, oh.messages)
	}
	oh.mutex.Unlock()

	if summarySnapshot != "" {
		sanitizedMessages = append(sanitizedMessages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: "Conversation summary so far: " + summarySnapshot,
		})
	}
	if len(historySnapshot) > 0 {
		if maxMemoryMessages <= 0 {
			maxMemoryMessages = defaultMaxMemoryMessages
		}
		if len(historySnapshot) > maxMemoryMessages {
			historySnapshot = historySnapshot[len(historySnapshot)-maxMemoryMessages:]
		}
		sanitizedMessages = append(sanitizedMessages, historySnapshot...)
	}
	if formatInstruction != "" {
		sanitizedMessages = append(sanitizedMessages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: formatInstruction,
		})
	}
	if estimatedMaxOutputChars > 0 {
		sanitizedMessages = append(sanitizedMessages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: fmt.Sprintf("Limit the assistant output to at most %d characters.", estimatedMaxOutputChars),
		})
	}

	sanitizedMessages = append(sanitizedMessages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: text,
	})
	request := openai.ChatCompletionRequest{
		Model:          model,
		N:              n,
		User:           requestID,
		ResponseFormat: responseFormat,
		Messages:       sanitizedMessages,
	}
	if options.MaxTokens > 0 {
		request.MaxTokens = options.MaxTokens
	}
	if options.Temperature != 0 {
		request.Temperature = options.Temperature
	}
	if options.TopP != 0 {
		request.TopP = options.TopP
	}
	if options.LogitBias != nil {
		request.LogitBias = options.LogitBias
	}
	response, err := oh.client.CreateChatCompletion(oh.ctx, request)
	if err != nil {
		return nil, err
	}
	choices := make([]QueryChoice, 0, len(response.Choices))
	for i, c := range response.Choices {
		content := c.Message.Content
		if requiresJSONWrapper {
			content = extractStructuredContent(content)
		}
		if options.FilterEmoji {
			content = emojiRegex.ReplaceAllString(content, "")
		}
		choices = append(choices, QueryChoice{
			Index:        i,
			Content:      content,
			FinishReason: string(c.FinishReason),
		})
	}
	assistantContent := ""
	if len(choices) > 0 {
		assistantContent = choices[0].Content
	}
	oh.mutex.Lock()
	oh.messages = append(oh.messages,
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: text},
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: assistantContent},
	)
	if maxMemoryMessages <= 0 {
		maxMemoryMessages = defaultMaxMemoryMessages
	}
	// Keep a hard cap by count as a safety measure.
	if len(oh.messages) > maxMemoryMessages {
		oh.messages = oh.messages[len(oh.messages)-maxMemoryMessages:]
	}

	// Async summarization trigger: count-based overflow.
	localSummary := oh.summary
	shouldStartSummarize := len(oh.messages) >= maxMemoryMessages
	var summarizeSnapshot []openai.ChatCompletionMessage
	if shouldStartSummarize && !oh.summarizing.Load() {
		oh.summarizing.Store(true)
		seq := atomic.AddUint64(&oh.summarizeSeq, 1)
		summarizeSnapshot = make([]openai.ChatCompletionMessage, len(oh.messages))
		copy(summarizeSnapshot, oh.messages)
		oh.mutex.Unlock()
		oh.startAsyncSummarizeIfNeeded(model, summarizeSnapshot, localSummary, seq)
	} else {
		oh.mutex.Unlock()
	}

	resp := &QueryResponse{
		Provider: oh.Provider(),
		Model:    response.Model,
		Choices:  choices,
		Usage: &TokenUsage{
			PromptTokens:     response.Usage.PromptTokens,
			CompletionTokens: response.Usage.CompletionTokens,
			TotalTokens:      response.Usage.TotalTokens,
			PromptTokensDetails: func() *PromptTokensDetails {
				if response.Usage.PromptTokensDetails == nil {
					return nil
				}
				return &PromptTokensDetails{
					AudioTokens:  response.Usage.PromptTokensDetails.AudioTokens,
					CachedTokens: response.Usage.PromptTokensDetails.CachedTokens,
				}
			}(),
			CompletionTokensDetails: func() *CompletionTokensDetails {
				if response.Usage.CompletionTokensDetails == nil {
					return nil
				}
				return &CompletionTokensDetails{
					AudioTokens:              response.Usage.CompletionTokensDetails.AudioTokens,
					ReasoningTokens:          response.Usage.CompletionTokensDetails.ReasoningTokens,
					AcceptedPredictionTokens: response.Usage.CompletionTokensDetails.AcceptedPredictionTokens,
					RejectedPredictionTokens: response.Usage.CompletionTokensDetails.RejectedPredictionTokens,
				}
			}(),
		},
	}

	llmDetails := &LLMDetails{
		RequestID:               requestID,
		Provider:                oh.Provider(),
		BaseURL:                 oh.baseUrl,
		Model:                   response.Model,
		Input:                   text,
		SystemPrompt:            oh.systemPrompt,
		N:                       n,
		MaxTokens:               options.MaxTokens,
		EstimatedMaxOutputChars: estimatedMaxOutputChars,
		FilterEmoji:             options.FilterEmoji,
		RequestedOutputFormat:   requestedOutputFormatLower,
		AppliedResponseFormat:   appliedResponseFormat,
		ResponseFormatApplied:   responseFormatApplied,
		ResponseID:              response.ID,
		Object:                  response.Object,
		Created:                 response.Created,
		SystemFingerprint:       response.SystemFingerprint,
		ChoicesCount:            len(response.Choices),
		Choices:                 resp.Choices,
		Usage:                   resp.Usage,
	}
	if b, e := json.Marshal(response.PromptFilterResults); e == nil {
		llmDetails.PromptFilterResultsJSON = string(b)
	}
	if b, e := json.Marshal(response.ServiceTier); e == nil {
		llmDetails.ServiceTierJSON = string(b)
	}
	if b, e := json.Marshal(response.Usage); e == nil {
		llmDetails.UsageRawJSON = string(b)
	}
	if b, e := json.Marshal(response.Choices); e == nil {
		llmDetails.ChoicesRawJSON = string(b)
	}
	if b, e := json.Marshal(response); e == nil {
		llmDetails.RawResponseJSON = string(b)
	}
	utils.Sig().Emit(constants.LLMUsage, "internal.llm", llmDetails)
	return resp, nil
}
