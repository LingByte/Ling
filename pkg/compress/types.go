package compress

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
	ErrEmptyInput         = errors.New("empty input")
	ErrUnsupportedMode    = errors.New("unsupported mode")
	ErrMissingLLM         = errors.New("missing llm")
	ErrMissingCompressed  = errors.New("missing compressed")
	ErrInvalidConstraints = errors.New("invalid constraints")
)

type Item struct {
	ID       string
	Title    string
	Content  string
	Tags     []string
	Metadata map[string]any
}

type CompressRequest struct {
	Query     string
	Items     []Item
	Mode      string
	MaxChars  int
	Separator string
	LLMModel  string

	// Strategy-specific options.
	Options map[string]any
}

type CompressResponse struct {
	OriginalChars   int
	CompressedChars int
	Compressed      string
	Strategy        string
	Latency         time.Duration
	Debug           map[string]any
}

type Compressor interface {
	Compress(ctx context.Context, req CompressRequest) (*CompressResponse, error)
}

type LLM interface {
	Query(text, model string) (string, error)
}

func DefaultRequest(items []Item) CompressRequest {
	return CompressRequest{
		Items:     items,
		Mode:      ModeRule,
		MaxChars:  2000,
		Separator: "\n\n",
	}
}

func joinBlocks(blocks []string, sep string) string {
	sep = strings.TrimSpace(sep)
	if sep == "" {
		sep = "\n\n"
	}
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		out = append(out, b)
	}
	return strings.Join(out, sep)
}
