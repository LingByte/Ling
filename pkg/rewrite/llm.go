package rewrite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type LLMRewriter struct {
	LLM   LLM
	Model string
}

type llmOut struct {
	Rewritten string   `json:"rewritten"`
	Keywords  []string `json:"keywords"`
}

func (r *LLMRewriter) Rewrite(ctx context.Context, req RewriteRequest) (*RewriteResponse, error) {
	_ = ctx
	start := time.Now()
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return nil, ErrEmptyQuery
	}
	if r == nil || r.LLM == nil {
		return nil, ErrMissingLLM
	}

	model := strings.TrimSpace(req.LLMModel)
	if model == "" {
		model = strings.TrimSpace(r.Model)
	}
	if model == "" {
		model = "gpt-4o-mini"
	}

	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 200
	}

	prompt := buildRewritePrompt(q, maxChars)
	out, err := r.LLM.Query(prompt, model)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, errors.New("llm returned empty rewrite")
	}

	var parsed llmOut
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		// If not JSON, treat output as rewritten query.
		rew := trimToMaxChars(out, maxChars)
		return &RewriteResponse{
			Original:  q,
			Rewritten: rew,
			Keywords:  nil,
			Strategy:  ModeLLM,
			Latency:   time.Since(start),
			Debug: map[string]any{
				"raw":         out,
				"parse_error": err.Error(),
			},
		}, nil
	}

	rew := trimToMaxChars(parsed.Rewritten, maxChars)
	kws := dedupKeepOrder(parsed.Keywords)

	if rew == "" {
		return nil, errors.New("llm returned empty rewritten")
	}

	return &RewriteResponse{
		Original:  q,
		Rewritten: rew,
		Keywords:  kws,
		Strategy:  ModeLLM,
		Latency:   time.Since(start),
		Debug: map[string]any{
			"raw": out,
		},
	}, nil
}

func buildRewritePrompt(query string, maxChars int) string {
	return "你是一个搜索/检索系统的查询重写器。\n" +
		"目标: 将原始查询改写为更清晰、信息密度更高、适合检索的查询。\n" +
		"要求: 不要添加编造事实; 保留原始意图; 不要输出多句废话。\n" +
		"输出: 只输出JSON。schema: {\"rewritten\":string,\"keywords\":[string]}\n" +
		"限制: rewritten 不超过" + itoa(maxChars) + "个字符。\n" +
		"原始查询: " + query + "\n"
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
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
