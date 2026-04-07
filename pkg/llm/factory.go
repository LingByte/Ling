package llm

import (
	"context"
	"strings"
)

const (
	ProviderOpenAI    = "openai"
	ProviderOllama    = "ollama"
	ProviderAlibaba   = "alibaba"
	ProviderAnthropic = "anthropic"
	ProviderLMStudio  = "lmstudio"
	ProviderCoze      = "coze"
)

func normalizeProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "", ProviderOpenAI:
		return ProviderOpenAI
	case ProviderOllama:
		return ProviderOllama
	case ProviderAlibaba:
		return ProviderAlibaba
	case ProviderAnthropic:
		return ProviderAnthropic
	case ProviderLMStudio:
		return ProviderLMStudio
	case ProviderCoze:
		return ProviderCoze
	default:
		return ProviderOpenAI
	}
}

// NewProviderHandler creates an LLM handler by provider type.
// Note: in Ling, non-OpenAI providers currently use OpenAI-compatible chat API shape.
func NewProviderHandler(ctx context.Context, provider string, llmOptions *LLMOptions) (LLMHandler, error) {
	if llmOptions == nil {
		llmOptions = &LLMOptions{}
	}
	selected := normalizeProvider(provider)
	if strings.TrimSpace(llmOptions.Provider) != "" {
		selected = normalizeProvider(llmOptions.Provider)
	}

	opts := *llmOptions

	switch selected {
	case ProviderOllama:
		if strings.TrimSpace(opts.BaseURL) == "" {
			opts.BaseURL = "http://localhost:11434/v1"
		}
		if strings.TrimSpace(opts.ApiKey) == "" {
			opts.ApiKey = "ollama"
		}
		return newOpenAICompatibleHandler(ctx, &opts, LLM_OLLAMA)
	case ProviderAlibaba:
		// DashScope OpenAI-compatible endpoint.
		if strings.TrimSpace(opts.BaseURL) == "" {
			opts.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		}
		return newOpenAICompatibleHandler(ctx, &opts, LLM_ALIBABA)
	case ProviderAnthropic:
		// Anthropic OpenAI-compatible endpoint.
		if strings.TrimSpace(opts.BaseURL) == "" {
			opts.BaseURL = "https://api.anthropic.com/v1"
		}
		return newOpenAICompatibleHandler(ctx, &opts, LLM_ANTHROPIC)
	case ProviderLMStudio:
		if strings.TrimSpace(opts.BaseURL) == "" {
			opts.BaseURL = "http://localhost:1234/v1"
		}
		if strings.TrimSpace(opts.ApiKey) == "" {
			opts.ApiKey = "lmstudio"
		}
		return newOpenAICompatibleHandler(ctx, &opts, LLM_LMSTUDIO)
	case ProviderCoze:
		// If user configures a Coze OpenAI-compatible gateway, it can be used directly.
		return newOpenAICompatibleHandler(ctx, &opts, LLM_COZE)
	default:
		return newOpenAICompatibleHandler(ctx, &opts, LLM_OPENAI)
	}
}

// NewLLMProvider provides a SoulNexus-like factory signature for Ling.
func NewLLMProvider(ctx context.Context, provider, apiKey, apiURL, systemPrompt string) (LLMHandler, error) {
	return NewProviderHandler(ctx, provider, &LLMOptions{
		Provider:     provider,
		ApiKey:       apiKey,
		BaseURL:      apiURL,
		SystemPrompt: systemPrompt,
	})
}

