package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/chain"
	"github.com/LingByte/Ling/pkg/compress"
	"github.com/LingByte/Ling/pkg/expand"
	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/retrieval"
	"github.com/LingByte/Ling/pkg/rewrite"
	"github.com/LingByte/Ling/pkg/utils"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	question := strings.TrimSpace(utils.GetEnv("QUERY"))
	if question == "" {
		question = "如何提高 Python 代码效率？"
	}

	hy, cleanup, err := buildHybridForDemo(ctx)
	if err != nil {
		fmt.Printf("init hybrid failed: %v\n", err)
		return
	}
	defer cleanup()

	rw, _ := rewrite.New(rewrite.ModeRule, nil)
	exp, _ := expand.New(expand.ModeRule, &expand.FactoryOptions{
		Synonyms: map[string][]string{
			"优化":   {"性能", "效率", "提速"},
			"python": {"py", "脚本"},
		},
	})
	cmp, _ := compress.New(compress.ModeRule, nil)

	answerLLM := mustBuildLLM(ctx)

	ch := chain.New(
		chain.RewriteStep{Rewriter: rw, Request: rewrite.RewriteRequest{MaxChars: 200}},
		chain.ExpandStep{Expander: exp, Request: expand.ExpandRequest{MaxTerms: 8, Separator: " "}, UseRewritten: true},
		chain.RouterStep{
			StepName: "route_retrieve",
			Select: func(ctx context.Context, st *chain.State) (string, error) {
				_ = ctx
				if hy != nil {
					return "hybrid", nil
				}
				return "fallback", nil
			},
			Routes: map[string]chain.Step{
				"hybrid": chain.RetryStep{
					StepName:    "retrieve_with_retry",
					Inner:       chain.RetrieveStep{Retriever: hy, Options: &knowledge.QueryOptions{TopK: 5}, UseExpanded: true},
					MaxAttempts: 2,
					Backoff: func(attempt int) time.Duration {
						return time.Duration(attempt) * 200 * time.Millisecond
					},
				},
				"fallback": chain.StepFunc{
					StepName: "fallback_retrieve",
					Fn: func(ctx context.Context, st *chain.State) error {
						_ = ctx
						st.Results = []knowledge.QueryResult{
							{
								Record: knowledge.Record{
									ID:      "fallback#1",
									Title:   "Fallback Knowledge",
									Content: "没有连上向量库，当前使用本地示例结果。",
								},
								Score: 0.5,
							},
						}
						return nil
					},
				},
			},
		},
		chain.CompressStep{Compressor: cmp, Request: compress.CompressRequest{MaxChars: 1200, Separator: "\n\n"}},
		chain.RouterStep{
			StepName: "route_answer",
			Select: func(ctx context.Context, st *chain.State) (string, error) {
				_ = ctx
				if answerLLM != nil {
					return "llm", nil
				}
				return "fallback", nil
			},
			Routes: map[string]chain.Step{
				"llm": chain.AnswerStep{LLM: answerLLM, Model: strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))},
				"fallback": chain.StepFunc{
					StepName: "fallback_answer",
					Fn: func(ctx context.Context, st *chain.State) error {
						_ = ctx
						st.Answer = "LLM 未配置，已完成 rewrite/expand/retrieve/compress，可以先查看上下文再配置 OPENAI 环境变量。"
						return nil
					},
				},
			},
		},
		chain.StepFunc{
			StepName: "print",
			Fn: func(ctx context.Context, st *chain.State) error {
				_ = ctx
				fmt.Printf("query: %s\n", st.Query)
				fmt.Printf("rewritten: %s\n", st.Rewritten)
				fmt.Printf("expanded: %s\n", st.Expanded)
				fmt.Printf("results: %d\n", len(st.Results))
				fmt.Printf("context chars: %d\n", len(st.Context))
				fmt.Printf("answer: %s\n", st.Answer)
				fmt.Printf("timings: %+v\n", st.Timings)
				return nil
			},
		},
	)

	st := &chain.State{
		Query: question,
	}
	if err := ch.Run(ctx, st); err != nil {
		fmt.Printf("chain run failed: %v\n", err)
	}
}

func buildHybridForDemo(ctx context.Context) (*retrieval.HybridHandler, func(), error) {
	qdrantURL := strings.TrimSpace(utils.GetEnv("QDRANT_URL"))
	collection := strings.TrimSpace(utils.GetEnv("QDRANT_COLLECTION"))
	nvURL := strings.TrimSpace(utils.GetEnv("NVIDIA_EMBEDDINGS_URL"))
	nvKey := strings.TrimSpace(utils.GetEnv("NVIDIA_API_KEY"))
	nvModel := strings.TrimSpace(utils.GetEnv("NVIDIA_EMBEDDINGS_MODEL"))
	if qdrantURL == "" || collection == "" || nvURL == "" || nvKey == "" || nvModel == "" {
		return nil, func() {}, fmt.Errorf("missing QDRANT_* or NVIDIA_* env vars")
	}

	embedder := &knowledge.NvidiaEmbedClient{
		BaseURL: nvURL,
		APIKey:  nvKey,
		Model:   nvModel,
	}
	vec, err := knowledge.New(knowledge.KnowledgeQdrant, &knowledge.FactoryOptions{
		Qdrant: &knowledge.QdrantOptions{
			BaseURL:    qdrantURL,
			APIKey:     strings.TrimSpace(utils.GetEnv("QDRANT_API_KEY")),
			Collection: collection,
			Embedder:   embedder,
		},
	})
	if err != nil {
		return nil, func() {}, err
	}

	hy, err := retrieval.NewHybrid(&retrieval.HybridOptions{
		KnowledgeProvider: vec.Provider(),
		Vector:            vec,
		IndexBasePath:     ".data/chain_pipeline_demo",
		Reranker:          buildReranker(),
	})
	if err != nil {
		return nil, func() {}, err
	}

	now := time.Now()
	seed := []knowledge.Record{
		{
			ID:        "chain_demo#1",
			Source:    "chain_pipeline_demo",
			Title:     "Python 性能实践",
			Content:   "优先使用内置函数、列表推导式与生成器；先用 timeit 和 cProfile 定位瓶颈，再优化热点代码。",
			Tags:      []string{"python", "performance", "benchmark"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "chain_demo#2",
			Source:    "chain_pipeline_demo",
			Title:     "通用优化原则",
			Content:   "避免过早优化，先测量后决策。减少不必要拷贝与重复 IO，提高缓存命中率。",
			Tags:      []string{"optimization"},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := hy.Upsert(ctx, seed, &knowledge.UpsertOptions{}); err != nil {
		_ = hy.Close()
		return nil, func() {}, err
	}

	cleanup := func() {
		_ = hy.Delete(context.Background(), []string{"chain_demo#1", "chain_demo#2"}, &knowledge.DeleteOptions{})
		_ = hy.Close()
	}
	return hy, cleanup, nil
}

func buildReranker() retrieval.Reranker {
	url := strings.TrimSpace(utils.GetEnv("SILICONFLOW_RERANK_URL"))
	key := strings.TrimSpace(utils.GetEnv("SILICONFLOW_API_KEY"))
	model := strings.TrimSpace(utils.GetEnv("SILICONFLOW_RERANK_MODEL"))
	if url == "" || key == "" || model == "" {
		return nil
	}
	return &knowledge.SiliconFlowRerankClient{BaseURL: url, APIKey: key, Model: model}
}

func mustBuildLLM(ctx context.Context) llm.LLMHandler {
	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	if apiKey == "" || baseURL == "" {
		return nil
	}
	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{
		ApiKey:       apiKey,
		BaseURL:      baseURL,
		SystemPrompt: "你是检索增强问答助手，只允许基于上下文作答。",
	})
	if err != nil {
		return nil
	}
	return h
}

