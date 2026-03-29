package compress

import "strings"

type FactoryOptions struct {
	LLM LLM
}

func New(mode string, opts *FactoryOptions) (Compressor, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeRule
	}

	switch mode {
	case ModeRule:
		return &RuleCompressor{}, nil
	case ModeLLM:
		if opts == nil || opts.LLM == nil {
			return nil, ErrMissingLLM
		}
		return &LLMCompressor{LLM: opts.LLM}, nil
	default:
		return nil, ErrUnsupportedMode
	}
}
