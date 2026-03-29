package censor

import "strings"

type FactoryOptions struct {
	LLM      LLM
	LLMModel string
	Policy   *Policy
	Rules    []Rule
	KeywordDictPath string
}

func New(mode string, opts *FactoryOptions) (Censor, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeRule
	}

	switch mode {
	case ModeRule:
		rules := DefaultRules()
		pol := DefaultPolicy()
		var keywordRules []KeywordRule
		if opts != nil {
			if len(opts.Rules) > 0 {
				rules = opts.Rules
			}
			if opts.Policy != nil {
				pol = opts.Policy
			}
			if strings.TrimSpace(opts.KeywordDictPath) != "" {
				kr, err := LoadKeywordDict(opts.KeywordDictPath, pol.CaseSensitive)
				if err != nil {
					return nil, err
				}
				keywordRules = kr
			}
		}
		return &RuleCensor{Policy: pol, Rules: rules, KeywordRules: keywordRules}, nil
	case ModeLLM:
		if opts == nil || opts.LLM == nil {
			return nil, ErrMissingLLM
		}
		pol := DefaultPolicy()
		if opts.Policy != nil {
			pol = opts.Policy
		}
		return &LLMCensor{LLM: opts.LLM, Model: opts.LLMModel, Policy: pol}, nil
	default:
		return nil, ErrUnsupportedMode
	}
}
