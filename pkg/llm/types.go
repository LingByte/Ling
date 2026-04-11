package llm

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

var requestCounter uint64

const (
	LLM_OPENAI    = "llm.openai"
	LLM_ANTHROPIC = "llm.anthropic"
	LLM_COZE      = "llm.coze"
	LLM_OLLAMA    = "llm.ollama"
	LLM_LMSTUDIO  = "llm.lmstudio"
	LLM_ALIBABA   = "llm.alibaba"
)

const (
	OutputFormatText       = "text"
	OutputFormatJSON       = "json"
	OutputFormatJSONObject = "json_object"
	OutputFormatJSONSchema = "json_schema"
	OutputFormatXML        = "xml"
	OutputFormatHTML       = "html"
	OutputFormatSQL        = "sql"
)

// LLMProvider common provider type
type LLMProvider string

// ToString toString for llm
func (lp LLMProvider) ToString() string {
	return string(lp)
}

type LLMOptions struct {
	Provider        string
	ApiKey          string
	BaseURL         string
	SystemPrompt    string
	FewShotExamples []FewShotExample
	logger          *zap.Logger
}

type FewShotExample struct {
	User      string
	Assistant string
}

type QueryOptions struct {
	Model            string
	N                int
	MaxTokens        int
	Temperature      float32
	TopP             float32
	LogitBias        map[string]int
	FilterEmoji      bool
	EnableJSONOutput bool
	OutputFormat     string
	logger           *zap.Logger
}

type TokenUsage struct {
	PromptTokens            int
	CompletionTokens        int
	TotalTokens             int
	PromptTokensDetails     *PromptTokensDetails
	CompletionTokensDetails *CompletionTokensDetails
}

type CompletionTokensDetails struct {
	AudioTokens              int
	ReasoningTokens          int
	AcceptedPredictionTokens int
	RejectedPredictionTokens int
}

type PromptTokensDetails struct {
	AudioTokens  int
	CachedTokens int
}

type QueryChoice struct {
	Index        int
	Content      string
	FinishReason string
}

type QueryResponse struct {
	Provider string
	Model    string
	Choices  []QueryChoice
	Usage    *TokenUsage
}

type LLMDetails struct {
	RequestID               string
	Provider                string
	BaseURL                 string
	Model                   string
	Input                   string
	SystemPrompt            string
	N                       int
	MaxTokens               int
	EstimatedMaxOutputChars int
	FilterEmoji             bool
	RequestedOutputFormat   string
	AppliedResponseFormat   string
	ResponseFormatApplied   bool
	ResponseID              string
	Object                  string
	Created                 int64
	SystemFingerprint       string
	PromptFilterResultsJSON string
	ServiceTierJSON         string
	ChoicesCount            int
	Choices                 []QueryChoice
	Usage                   *TokenUsage
	UsageRawJSON            string
	ChoicesRawJSON          string
	RawResponseJSON         string
}

// LLMHandler common llm hanlder interface
type LLMHandler interface {
	Query(text, model string) (string, error)

	QueryWithOptions(text string, options *QueryOptions) (*QueryResponse, error)

	QueryStream(text string, options *QueryOptions, callback func(segment string, isComplete bool) error) (*QueryResponse, error)

	Provider() string

	Interrupt()

	ResetMemory()

	SummarizeMemory(model string) (string, error)

	SetMaxMemoryMessages(n int)

	GetMaxMemoryMessages() int
}

func GenerateLingRequestID() string {
	host, _ := os.Hostname()
	c := atomic.AddUint64(&requestCounter, 1)
	return fmt.Sprintf("ling-%s-%d-%d-%d", host, os.Getpid(), time.Now().UnixNano(), c)
}
