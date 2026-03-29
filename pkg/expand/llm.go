package expand

import (
	"context"
	"errors"
	"strings"
	"time"
)

type LLMExpander struct {
	LLM LLM
}

func (e *LLMExpander) Expand(ctx context.Context, req ExpandRequest) (*ExpandResponse, error) {
	_ = ctx
	start := time.Now()
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return nil, ErrEmptyQuery
	}
	if e == nil || e.LLM == nil {
		return nil, ErrMissingLLM
	}

	model := strings.TrimSpace(req.LLMModel)
	if model == "" {
		model = "gpt-4o-mini"
	}

	maxTerms := req.MaxTerms
	if maxTerms <= 0 {
		maxTerms = 12
	}

	prompt := buildExpandPrompt(q, maxTerms)
	out, err := e.LLM.Query(prompt, model)
	if err != nil {
		return nil, err
	}

	terms := parseBarSeparatedTerms(out)
	terms = dedupKeepOrder(terms)
	if len(terms) > maxTerms {
		terms = terms[:maxTerms]
	}

	if len(terms) == 0 {
		return nil, errors.New("llm returned empty expansion")
	}

	return &ExpandResponse{
		Original: q,
		Terms:    terms,
		Expanded: joinExpanded(q, terms, req.Separator),
		Strategy: ModeLLM,
		Latency:  time.Since(start),
		Debug: map[string]any{
			"raw": strings.TrimSpace(out),
		},
	}, nil
}

func buildExpandPrompt(query string, maxTerms int) string {
	// Keep the prompt stable and concise. We expect output as bar-separated terms.
	return "对于<原始查询>,补充其上位概念、下位具体场景及相关关联词,用|分隔。\n" +
		"原始查询:" + query + "\n" +
		"要求: 输出不超过" + itoa(maxTerms) + "个词条; 只输出词条,不要解释。\n" +
		"示例: 跑步→运动|慢跑|马拉松|跑鞋|运动手环\n"
}

func parseBarSeparatedTerms(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// common patterns: "跑步→运动|慢跑..." or just "运动|慢跑..."
	if idx := strings.Index(s, "→"); idx >= 0 {
		s = s[idx+len("→"):]
	}
	// Some models may output newlines; normalize to single line.
	s = strings.ReplaceAll(s, "\n", "|")
	parts := strings.Split(s, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "\"“”'` ")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 12)
	for n > 0 {
		d := n % 10
		buf = append(buf, byte('0'+d))
		n /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
