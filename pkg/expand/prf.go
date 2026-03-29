package expand

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"
)

type PRFExpander struct{}

func (e *PRFExpander) Expand(ctx context.Context, req ExpandRequest) (*ExpandResponse, error) {
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

	fb := req.Feedback
	if fb == nil {
		fb = &FeedbackContext{}
	}

	// Collect candidate tokens from snippets + tags.
	counts := map[string]int{}
	for _, snip := range fb.TopSnippets {
		for _, tok := range tokenizeForPRF(snip) {
			counts[tok]++
		}
	}
	for _, tag := range fb.TopTags {
		t := normalizeToken(tag)
		if t == "" {
			continue
		}
		// Tags are usually high quality: give them more weight.
		counts[t] += 3
	}

	// Remove tokens already in query.
	qTokens := map[string]struct{}{}
	for _, tok := range tokenizeForPRF(q) {
		qTokens[tok] = struct{}{}
	}

	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(counts))
	for k, v := range counts {
		if _, ok := qTokens[k]; ok {
			continue
		}
		// very short tokens are noisy
		if len([]rune(k)) <= 1 {
			continue
		}
		items = append(items, kv{k: k, v: v})
	}
	if len(items) == 0 {
		return &ExpandResponse{
			Original: q,
			Terms:    nil,
			Expanded: q,
			Strategy: ModePRF,
			Latency:  time.Since(start),
			Debug: map[string]any{
				"reason": "no_terms",
			},
		}, nil
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].v == items[j].v {
			return items[i].k < items[j].k
		}
		return items[i].v > items[j].v
	})

	terms := make([]string, 0, maxTerms)
	for _, it := range items {
		terms = append(terms, it.k)
		if len(terms) >= maxTerms {
			break
		}
	}

	return &ExpandResponse{
		Original: q,
		Terms:    terms,
		Expanded: joinExpanded(q, terms, req.Separator),
		Strategy: ModePRF,
		Latency:  time.Since(start),
		Debug: map[string]any{
			"candidates": len(items),
		},
	}, nil
}

var rePRFToken = regexp.MustCompile(`[\p{Han}]{1,6}|[A-Za-z0-9_]{2,}`)

func tokenizeForPRF(s string) []string {
	ms := rePRFToken.FindAllString(s, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		m = normalizeToken(m)
		if m == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func normalizeToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.Trim(s, "\"“”'` ")
	return s
}
