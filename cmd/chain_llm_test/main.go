package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/chain"
	"github.com/LingByte/Ling/pkg/censor"
	"github.com/LingByte/Ling/pkg/compress"
	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/utils"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		fmt.Printf("missing OPENAI_API_KEY/OPENAI_BASE_URL/OPENAI_MODEL\n")
		return
	}

	question := strings.TrimSpace(utils.GetEnv("QUERY"))
	if question == "" {
		question = "如何提高Python代码效率？"
	}

	fewShot := []llm.FewShotExample{
		{User: "请根据资料回答：什么是列表推导式？", Assistant: "列表推导式是一种用简洁语法从可迭代对象生成列表的写法。\n如果资料中没有定义，请回复：未找到相关信息"},
	}

	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{
		ApiKey:          apiKey,
		BaseURL:         baseURL,
		SystemPrompt:    "你是一个严谨的检索增强问答助手。",
		FewShotExamples: fewShot,
	})
	if err != nil {
		fmt.Printf("llm init failed: %v\n", err)
		return
	}

	// Simulated retrieval results (so you can test chain+LLM without Qdrant).
	results := []knowledge.QueryResult{
		{Record: knowledge.Record{ID: "doc#1", Title: "Python 性能优化", Content: "循环优化：优先使用内置函数（sum、map）或列表推导式替代显式 for 循环。\n内存管理：使用生成器减少峰值内存；避免不必要的中间列表。", Tags: []string{"python", "性能优化"}}, Score: 0.92},
		{Record: knowledge.Record{ID: "doc#2", Title: "性能分析", Content: "性能评测：可使用 timeit 做微基准，使用 cProfile 定位热点函数。\n优化优先级：先测量再优化，避免过早优化。", Tags: []string{"benchmark"}}, Score: 0.87},
	}

	cmp, _ := compress.New(compress.ModeRule, nil)
	cs, _ := censor.New(censor.ModeRule, &censor.FactoryOptions{KeywordDictPath: "pkg/censor/keyword_dict.txt"})

	ch := chain.New(
		chain.StepFunc{StepName: "seed", Fn: func(ctx context.Context, s *chain.State) error {
			_ = ctx
			s.Results = results
			return nil
		}},
		chain.CompressStep{Compressor: cmp, Request: compress.CompressRequest{MaxChars: 1200, Separator: "\n\n"}},
		chain.CensorStep{Censor: cs, Request: censor.AssessRequest{Mode: censor.ModeRule}, Target: ""},
		chain.AnswerStep{LLM: h, Model: model},
		chain.StepFunc{StepName: "print", Fn: func(ctx context.Context, s *chain.State) error {
			_ = ctx
			fmt.Printf("question=%q\n", s.Query)
			fmt.Printf("context_chars=%d\n", len(s.Context))
			fmt.Printf("answer:\n%s\n", strings.TrimSpace(s.Answer))
			fmt.Printf("timings=%v\n", s.Timings)
			return nil
		}},
	)

	st := &chain.State{Query: question}
	if err := ch.Run(ctx, st); err != nil {
		fmt.Printf("chain run error: %v\n", err)
		return
	}
}
