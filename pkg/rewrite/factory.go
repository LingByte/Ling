package rewrite

import "strings"

type FactoryOptions struct {
	LLM      LLM
	LLMModel string
}

func New(mode string, opts *FactoryOptions) (Rewriter, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeRule
	}
	switch mode {
	case ModeRule:
		return &RuleRewriter{}, nil
	case ModeLLM:
		if opts == nil || opts.LLM == nil {
			return nil, ErrMissingLLM
		}
		return &LLMRewriter{LLM: opts.LLM, Model: opts.LLMModel}, nil
	default:
		return nil, ErrUnsupportedRewriter
	}
}
