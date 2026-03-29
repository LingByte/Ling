package expand

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	ModeRule = "rule"
	ModeLLM  = "llm"
	ModePRF  = "prf"
)

var (
	ErrEmptyQuery          = errors.New("empty query")
	ErrUnsupportedExpander = errors.New("unsupported expander")
	ErrMissingLLM          = errors.New("missing llm")
)

type FeedbackContext struct {
	TopSnippets []string
	TopTags     []string
}

type ExpandRequest struct {
	Query     string
	Mode      string
	MaxTerms  int
	Separator string

	LLMModel string
	Feedback *FeedbackContext

	// Strategy-specific options.
	Options map[string]any
}

type ExpandResponse struct {
	Original string
	Expanded string
	Terms    []string
	Strategy string
	Latency  time.Duration
	Debug    map[string]any
}

type Expander interface {
	Expand(ctx context.Context, req ExpandRequest) (*ExpandResponse, error)
}

type LLM interface {
	Query(text, model string) (string, error)
}

func DefaultRequest(query string) ExpandRequest {
	return ExpandRequest{
		Query:     query,
		Mode:      ModeRule,
		MaxTerms:  8,
		Separator: " ",
	}
}

func joinExpanded(original string, terms []string, sep string) string {
	sep = strings.TrimSpace(sep)
	if sep == "" {
		sep = " "
	}
	parts := make([]string, 0, 1+len(terms))
	parts = append(parts, strings.TrimSpace(original))
	for _, t := range terms {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, sep)
}
