package expand

import "strings"

type FactoryOptions struct {
	LLM      LLM
	Synonyms map[string][]string
}

func New(mode string, opts *FactoryOptions) (Expander, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeRule
	}

	switch mode {
	case ModeRule:
		syn := map[string][]string{}
		if opts != nil && opts.Synonyms != nil {
			syn = opts.Synonyms
		}
		return &RuleExpander{Synonyms: syn}, nil
	case ModeLLM:
		if opts == nil || opts.LLM == nil {
			return nil, ErrMissingLLM
		}
		return &LLMExpander{LLM: opts.LLM}, nil
	case ModePRF:
		return &PRFExpander{}, nil
	default:
		return nil, ErrUnsupportedExpander
	}
}
