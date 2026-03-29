package rewrite

import (
	"context"
	"regexp"
	"strings"
	"time"
)

type RuleRewriter struct{}

func (r *RuleRewriter) Rewrite(ctx context.Context, req RewriteRequest) (*RewriteResponse, error) {
	_ = ctx
	start := time.Now()
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return nil, ErrEmptyQuery
	}

	// Normalize whitespace and some punctuation.
	rewritten := q
	rewritten = strings.ReplaceAll(rewritten, "，", ",")
	rewritten = strings.ReplaceAll(rewritten, "？", "?")
	rewritten = strings.ReplaceAll(rewritten, "：", ":")
	rewritten = strings.ReplaceAll(rewritten, "；", ";")
	rewritten = strings.ReplaceAll(rewritten, "（", "(")
	rewritten = strings.ReplaceAll(rewritten, "）", ")")
	rewritten = reSpaces.ReplaceAllString(rewritten, " ")
	rewritten = strings.TrimSpace(rewritten)
	rewritten = trimToMaxChars(rewritten, req.MaxChars)

	return &RewriteResponse{
		Original:  q,
		Rewritten: rewritten,
		Keywords:  nil,
		Strategy:  ModeRule,
		Latency:   time.Since(start),
		Debug: map[string]any{
			"normalized": rewritten != q,
		},
	}, nil
}

var reSpaces = regexp.MustCompile(`\s+`)
