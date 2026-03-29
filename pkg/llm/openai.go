package llm

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/LingByte/Ling/pkg/constants"
	"github.com/LingByte/Ling/pkg/utils"
	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

var emojiRegex = regexp.MustCompile(`[\x{00A9}\x{00AE}\x{203C}\x{2049}\x{2122}\x{2139}\x{2194}-\x{2199}\x{21A9}-\x{21AA}\x{231A}-\x{231B}\x{2328}\x{23CF}\x{23E9}-\x{23F3}\x{23F8}-\x{23FA}\x{24C2}\x{25AA}-\x{25AB}\x{25B6}\x{25C0}\x{25FB}-\x{25FE}\x{2600}-\x{26FF}\x{2700}-\x{27BF}\x{2B05}-\x{2B07}\x{2B1B}-\x{2B1C}\x{2B50}\x{2B55}\x{3030}\x{303D}\x{3297}\x{3299}\x{1F004}\x{1F0CF}\x{1F170}-\x{1F251}\x{1F300}-\x{1F5FF}\x{1F600}-\x{1F64F}\x{1F680}-\x{1F6FF}\x{1F910}-\x{1F93E}\x{1F940}-\x{1F94C}\x{1F950}-\x{1F96B}\x{1F980}-\x{1F997}\x{1F9C0}-\x{1F9E6}\x{1FA70}-\x{1FA74}\x{1FA78}-\x{1FA7A}\x{1FA80}-\x{1FA86}\x{1FA90}-\x{1FAA8}\x{1FAB0}-\x{1FAB6}\x{1FAC0}-\x{1FAC2}\x{1FAD0}-\x{1FAD6}\x{1F1E6}-\x{1F1FF}\x{200D}\x{FE0F}]`)

type OpenaiHandler struct {
	ctx          context.Context
	client       *openai.Client
	systemPrompt string
	model        string
	baseUrl      string
	logger       *zap.Logger
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
		ctx:          ctx,
		client:       client,
		baseUrl:      llmOptions.BaseURL,
		systemPrompt: llmOptions.SystemPrompt,
		logger:       llmOptions.logger,
	}, nil
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
	if formatInstruction != "" {
		sanitizedMessages = append(sanitizedMessages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: formatInstruction,
		})
	}
	sanitizedMessages = append(sanitizedMessages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: text,
	})
	request := openai.ChatCompletionRequest{
		Model:          options.Model,
		N:              n,
		User:           requestID,
		ResponseFormat: responseFormat,
		Messages:       sanitizedMessages,
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
		RequestID:             requestID,
		Provider:              oh.Provider(),
		BaseURL:               oh.baseUrl,
		Model:                 response.Model,
		Input:                 text,
		SystemPrompt:          oh.systemPrompt,
		N:                     n,
		FilterEmoji:           options.FilterEmoji,
		RequestedOutputFormat: requestedOutputFormatLower,
		AppliedResponseFormat: appliedResponseFormat,
		ResponseFormatApplied: responseFormatApplied,
		ResponseID:            response.ID,
		Object:                response.Object,
		Created:               response.Created,
		SystemFingerprint:     response.SystemFingerprint,
		ChoicesCount:          len(response.Choices),
		Choices:               resp.Choices,
		Usage:                 resp.Usage,
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
