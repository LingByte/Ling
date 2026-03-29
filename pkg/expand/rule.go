package expand

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"
)

type RuleExpander struct {
	// Synonyms maps a trigger term to expansion terms.
	Synonyms map[string][]string
}

func (e *RuleExpander) Expand(ctx context.Context, req ExpandRequest) (*ExpandResponse, error) {
	_ = ctx
	start := time.Now()
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return nil, ErrEmptyQuery
	}

	maxTerms := req.MaxTerms
	if maxTerms <= 0 {
		maxTerms = 8
	}

	terms := make([]string, 0, maxTerms)
	debug := map[string]any{}

	// 1) Exact key substring match
	hitKeys := make([]string, 0)
	for k, vs := range e.Synonyms {
		k2 := strings.TrimSpace(k)
		if k2 == "" {
			continue
		}
		if strings.Contains(q, k2) {
			hitKeys = append(hitKeys, k2)
			for _, v := range vs {
				terms = append(terms, strings.TrimSpace(v))
			}
		}
	}
	if len(hitKeys) > 0 {
		sort.Strings(hitKeys)
		debug["hit_keys"] = hitKeys
	}

	// 2) Fallback: simple token triggers (CJK+latin words)
	if len(terms) == 0 {
		toks := tokenizeLoose(q)
		for _, tok := range toks {
			if vs, ok := e.Synonyms[tok]; ok {
				for _, v := range vs {
					terms = append(terms, strings.TrimSpace(v))
				}
			}
		}
	}

	terms = dedupKeepOrder(terms)
	if len(terms) > maxTerms {
		terms = terms[:maxTerms]
	}

	resp := &ExpandResponse{
		Original: q,
		Terms:    terms,
		Expanded: joinExpanded(q, terms, req.Separator),
		Strategy: ModeRule,
		Latency:  time.Since(start),
		Debug:    debug,
	}
	return resp, nil
}

var reLooseToken = regexp.MustCompile(`[\p{Han}]{1,6}|[A-Za-z0-9_]{2,}`)

func tokenizeLoose(s string) []string {
	ms := reLooseToken.FindAllString(s, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func dedupKeepOrder(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
