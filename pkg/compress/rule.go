package compress

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"
)

type RuleCompressor struct{}

func (c *RuleCompressor) Compress(ctx context.Context, req CompressRequest) (*CompressResponse, error) {
	_ = ctx
	start := time.Now()

	if len(req.Items) == 0 {
		return nil, ErrEmptyInput
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 2000
	}

	queryTokens := tokenize(req.Query)
	querySet := map[string]struct{}{}
	for _, t := range queryTokens {
		querySet[t] = struct{}{}
	}

	blocks := make([]string, 0, len(req.Items))
	debug := map[string]any{}
	totalIn := 0

	for _, it := range req.Items {
		totalIn += len(it.Content)
		b := compressItem(it, querySet)
		if b != "" {
			blocks = append(blocks, b)
		}
	}

	out := joinBlocks(blocks, req.Separator)
	if len(out) > maxChars {
		out = out[:maxChars]
		out = strings.TrimSpace(out)
	}
	if strings.TrimSpace(out) == "" {
		return nil, ErrMissingCompressed
	}

	debug["items"] = len(req.Items)
	debug["query_tokens"] = len(queryTokens)

	return &CompressResponse{
		OriginalChars:   totalIn,
		CompressedChars: len(out),
		Compressed:      out,
		Strategy:        ModeRule,
		Latency:         time.Since(start),
		Debug:           debug,
	}, nil
}

type scoredSentence struct {
	idx   int
	score int
	text  string
}

func compressItem(it Item, querySet map[string]struct{}) string {
	content := strings.TrimSpace(it.Content)
	if content == "" {
		return ""
	}

	sents := splitSentences(content)
	scored := make([]scoredSentence, 0, len(sents))
	for i, s := range sents {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		score := 0
		for _, tok := range tokenize(s) {
			if _, ok := querySet[tok]; ok {
				score += 2
			}
		}
		// Small preference for earlier sentences.
		if i < 2 {
			score++
		}
		// Prefer tags (when present) by lightly boosting if tag appears.
		for _, tg := range it.Tags {
			tg = strings.TrimSpace(tg)
			if tg == "" {
				continue
			}
			if strings.Contains(s, tg) {
				score++
			}
		}
		scored = append(scored, scoredSentence{idx: i, score: score, text: s})
	}

	// If query is empty, keep the first few sentences.
	if len(querySet) == 0 {
		keep := takeHead(sents, 3)
		return formatItemBlock(it, joinBlocks(keep, " "))
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].idx < scored[j].idx
		}
		return scored[i].score > scored[j].score
	})

	k := 3
	if len(scored) < k {
		k = len(scored)
	}
	picked := scored[:k]
	sort.Slice(picked, func(i, j int) bool { return picked[i].idx < picked[j].idx })

	lines := make([]string, 0, len(picked))
	for _, p := range picked {
		lines = append(lines, p.text)
	}
	return formatItemBlock(it, joinBlocks(lines, " "))
}

func formatItemBlock(it Item, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	head := strings.TrimSpace(it.Title)
	if head == "" {
		head = strings.TrimSpace(it.ID)
	}
	if head != "" {
		return head + "\n" + body
	}
	return body
}

var reSentence = regexp.MustCompile(`[^\n。！？!?]+[。！？!?]?`)

func splitSentences(s string) []string {
	// Prefer splitting by punctuation, keep order.
	ms := reSentence.FindAllString(s, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		out = append(out, m)
	}
	if len(out) > 0 {
		return out
	}
	// Fallback: newline split
	parts := strings.Split(s, "\n")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

var reToken = regexp.MustCompile(`[\p{Han}]{1,6}|[A-Za-z0-9_]{2,}`)

func tokenize(s string) []string {
	ms := reToken.FindAllString(s, -1)
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

func takeHead(in []string, n int) []string {
	out := make([]string, 0, n)
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
		if len(out) >= n {
			break
		}
	}
	return out
}
