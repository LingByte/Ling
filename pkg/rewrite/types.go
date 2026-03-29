package rewrite

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	ModeRule = "rule"
	ModeLLM  = "llm"
)

var (
	ErrEmptyQuery          = errors.New("empty query")
	ErrUnsupportedRewriter = errors.New("unsupported rewriter")
	ErrMissingLLM          = errors.New("missing llm")
)

type RewriteRequest struct {
	Query string
	Mode  string

	LLMModel string
	MaxChars int

	Options map[string]any
}

type RewriteResponse struct {
	Original  string
	Rewritten string
	Keywords  []string
	Strategy  string
	Latency   time.Duration
	Debug     map[string]any
}

type Rewriter interface {
	Rewrite(ctx context.Context, req RewriteRequest) (*RewriteResponse, error)
}

type LLM interface {
	Query(text, model string) (string, error)
}

func DefaultRequest(query string) RewriteRequest {
	return RewriteRequest{Query: query, Mode: ModeRule, MaxChars: 200}
}

func trimToMaxChars(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if maxChars > 0 && len(s) > maxChars {
		s = strings.TrimSpace(s[:maxChars])
	}
	return s
}
