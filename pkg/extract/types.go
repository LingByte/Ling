package extract

import (
	"context"
	"errors"
)

var (
	ErrEmptyText            = errors.New("empty text")
	ErrUnsupportedExtractor = errors.New("unsupported extractor")
	ErrMissingLLM           = errors.New("missing llm")
)

type ChunkInput struct {
	DocumentTitle string
	FileName      string
	Source        string
	Namespace     string
	ChunkIndex    int
	Text          string
}

type ExtractResult struct {
	Tags     []string
	Metadata map[string]any
}

type Options struct {
	MaxTags int
}

type Extractor interface {
	Provider() string
	Extract(ctx context.Context, in ChunkInput, opts *Options) (*ExtractResult, error)
}
