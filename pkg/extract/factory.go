package extract

import (
	"strings"

	"github.com/LingByte/Ling/pkg/llm"
)

const (
	ExtractorRule = "rule"
	ExtractorLLM  = "llm"
)

type FactoryOptions struct {
	LLM   llm.LLMHandler
	Model string
}

func New(kind string, opts *FactoryOptions) (Extractor, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		k = ExtractorRule
	}
	switch k {
	case ExtractorRule:
		return &RuleExtractor{}, nil
	case ExtractorLLM:
		if opts == nil || opts.LLM == nil {
			return nil, ErrMissingLLM
		}
		return &LLMExtractor{LLM: opts.LLM, Model: opts.Model}, nil
	default:
		return nil, ErrUnsupportedExtractor
	}
}
