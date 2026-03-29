package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/expand"
	"github.com/LingByte/Ling/pkg/utils"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	q := strings.TrimSpace(utils.GetEnv("QUERY"))
	if q == "" {
		q = "如何提高Python代码效率"
	}

	mode := strings.ToLower(strings.TrimSpace(utils.GetEnv("MODE")))
	if mode == "" {
		mode = expand.ModeRule
	}

	sep := utils.GetEnv("SEP")
	if strings.TrimSpace(sep) == "" {
		sep = " "
	}

	maxTerms := getEnvInt("MAX_TERMS", 8)

	var (
		opts *expand.FactoryOptions
		req  = expand.ExpandRequest{Query: q, Mode: mode, Separator: sep, MaxTerms: maxTerms}
	)

	switch mode {
	case expand.ModeRule:
		opts = &expand.FactoryOptions{Synonyms: map[string][]string{"减肥": {"瘦身", "控制体重", "减脂"}}}
	case expand.ModeLLM:
		opts = &expand.FactoryOptions{LLM: &fakeLLM{resp: "Python代码优化→循环效率|内存管理|常用库|性能对比"}}
		req.LLMModel = strings.TrimSpace(utils.GetEnv("LLM_MODEL"))
		if req.LLMModel == "" {
			req.LLMModel = "fake"
		}
	case expand.ModePRF:
		snips := splitNonEmpty(utils.GetEnv("PRF_SNIPPETS"), "||")
		tags := splitNonEmpty(utils.GetEnv("PRF_TAGS"), ",")
		if len(snips) == 0 {
			snips = []string{"Python 代码 优化 技巧 循环 效率 内存 管理 常用库 性能 对比"}
		}
		if len(tags) == 0 {
			tags = []string{"python", "性能优化"}
		}
		req.Feedback = &expand.FeedbackContext{TopSnippets: snips, TopTags: tags}
	default:
		fmt.Printf("unsupported MODE=%s\n", mode)
		return
	}

	exp, err := expand.New(mode, opts)
	if err != nil {
		fmt.Printf("new expander failed: %v\n", err)
		return
	}

	resp, err := exp.Expand(ctx, req)
	if err != nil {
		fmt.Printf("expand failed: %v\n", err)
		return
	}

	fmt.Printf("mode=%s\n", mode)
	fmt.Printf("original=%q\n", resp.Original)
	fmt.Printf("expanded=%q\n", resp.Expanded)
	fmt.Printf("terms=%v\n", resp.Terms)
	fmt.Printf("latency=%s\n", resp.Latency)
	if len(resp.Debug) > 0 {
		fmt.Printf("debug=%v\n", resp.Debug)
	}
}

func getEnvInt(key string, def int) int {
	v := strings.TrimSpace(utils.GetEnv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func splitNonEmpty(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
