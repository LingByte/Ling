package compress

import (
	"context"
	"errors"
	"strings"
	"time"
)

type LLMCompressor struct {
	LLM LLM
}

func (c *LLMCompressor) Compress(ctx context.Context, req CompressRequest) (*CompressResponse, error) {
	_ = ctx
	start := time.Now()

	if len(req.Items) == 0 {
		return nil, ErrEmptyInput
	}
	if c == nil || c.LLM == nil {
		return nil, ErrMissingLLM
	}

	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 2000
	}

	model := strings.TrimSpace(req.LLMModel)
	if model == "" {
		model = "gpt-4o-mini"
	}

	input := buildKnowledgeInput(req.Items, 12000)
	if strings.TrimSpace(input) == "" {
		return nil, ErrEmptyInput
	}

	prompt := buildCompressPrompt(req.Query, input, maxChars)
	out, err := c.LLM.Query(prompt, model)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, errors.New("llm returned empty compression")
	}
	if len(out) > maxChars {
		out = strings.TrimSpace(out[:maxChars])
	}

	origChars := 0
	for _, it := range req.Items {
		origChars += len(it.Content)
	}

	return &CompressResponse{
		OriginalChars:   origChars,
		CompressedChars: len(out),
		Compressed:      out,
		Strategy:        ModeLLM,
		Latency:         time.Since(start),
		Debug: map[string]any{
			"raw": out,
		},
	}, nil
}

func buildCompressPrompt(query string, knowledge string, maxChars int) string {
	query = strings.TrimSpace(query)
	if query == "" {
		query = "（无明确问题）"
	}
	return "你是一个检索增强问答系统的提示压缩器。\n" +
		"目标: 在不丢失关键事实和可引用信息的前提下，把知识库内容压缩成更短的上下文。\n" +
		"问题/意图: " + query + "\n" +
		"约束: 输出不超过" + itoa(maxChars) + "个字符；尽量保留数字、时间、地点、结论、条件、例外。\n" +
		"输出格式: 使用紧凑的要点列表（每行一个要点）。不要输出无关解释。\n" +
		"\n" +
		"知识库内容:\n" + knowledge + "\n"
}

func buildKnowledgeInput(items []Item, maxChars int) string {
	blocks := make([]string, 0, len(items))
	used := 0
	for _, it := range items {
		b := strings.TrimSpace(it.Content)
		if b == "" {
			continue
		}
		head := strings.TrimSpace(it.Title)
		if head == "" {
			head = strings.TrimSpace(it.ID)
		}
		if head != "" {
			b = head + "\n" + b
		}
		if used+len(b) > maxChars {
			remain := maxChars - used
			if remain <= 0 {
				break
			}
			b = b[:remain]
			blocks = append(blocks, b)
			used += len(b)
			break
		}
		blocks = append(blocks, b)
		used += len(b)
	}
	return joinBlocks(blocks, "\n\n---\n\n")
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
