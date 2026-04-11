// dialogue_optimize_chain 演示与 RAG 无关的「对话优化」链路：rewrite → expand →
// router → retry → 组装输出。不包含 RetrieveStep / CompressStep / AnswerStep。
//
// 运行：在 Ling 模块根目录执行
//
//	go run ./cmd/dialogue_optimize_chain
//
// 环境变量：
//   - QUERY：用户原话（默认一条中文示例）
//   - 规则模式（默认）：不配置 LLM 相关变量即可
//   - LLM 模式：设置 USE_LLM=1（或显式设置 LLM_PROVIDER），并配置对应 ApiKey、BaseURL、LLM_MODEL
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/chain"
	"github.com/LingByte/Ling/pkg/expand"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/rewrite"
	"github.com/LingByte/Ling/pkg/utils"
)

var errTransientNormalize = errors.New("transient normalize")

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	query := strings.TrimSpace(utils.GetEnv("QUERY"))
	if query == "" {
		query = "那个啥，帮我把下面这句说专业点：咱们这边想尽快对齐一下需求，别拖了。"
	}

	rewriteMode := rewrite.ModeRule
	expandMode := expand.ModeRule
	var rwFact *rewrite.FactoryOptions
	var exFact *expand.FactoryOptions
	model := strings.TrimSpace(utils.GetEnv("LLM_MODEL"))

	if h, m, ok := tryLLM(ctx); ok {
		rewriteMode = rewrite.ModeLLM
		expandMode = expand.ModeLLM
		rwFact = &rewrite.FactoryOptions{LLM: h, LLMModel: m}
		exFact = &expand.FactoryOptions{LLM: h}
		if model == "" {
			model = m
		}
	}

	rw, err := rewrite.New(rewriteMode, rwFact)
	if err != nil {
		fmt.Printf("rewrite init: %v\n", err)
		return
	}
	ex, err := expand.New(expandMode, exFact)
	if err != nil {
		fmt.Printf("expand init: %v\n", err)
		return
	}

	ch := chain.New(
		chain.RewriteStep{Rewriter: rw, Request: rewrite.RewriteRequest{
			Mode: rewriteMode, LLMModel: model, MaxChars: 400,
		}},
		chain.ExpandStep{Expander: ex, Request: expand.ExpandRequest{
			Mode: expandMode, LLMModel: model, MaxTerms: 12, Separator: " | ",
		}, UseRewritten: true},
		chain.RouterStep{
			StepName: "dialogue_style",
			Select: func(_ context.Context, st *chain.State) (string, error) {
				base := st.Rewritten
				if strings.TrimSpace(base) == "" {
					base = st.Query
				}
				if len([]rune(base)) < 48 {
					return "brief", nil
				}
				return "detailed", nil
			},
			Routes: map[string]chain.Step{
				"brief": chain.StepFunc{StepName: "style_brief", Fn: func(_ context.Context, st *chain.State) error {
					st.Meta["dialogue_style"] = "brief_clarify"
					return nil
				}},
				"detailed": chain.StepFunc{StepName: "style_detailed", Fn: func(_ context.Context, st *chain.State) error {
					st.Meta["dialogue_style"] = "structured_rewrite"
					return nil
				}},
			},
		},
		chain.RetryStep{
			StepName: "normalize",
			Inner: chain.StepFunc{StepName: "normalize_inner", Fn: func(_ context.Context, st *chain.State) error {
				n, _ := st.Meta["normalize_try"].(int)
				n++
				st.Meta["normalize_try"] = n
				if n < 2 {
					return errTransientNormalize
				}
				st.Meta["normalize_ok"] = true
				return nil
			}},
			MaxAttempts: 3,
			ShouldRetry: func(err error) bool { return errors.Is(err, errTransientNormalize) },
			Backoff:     func(int) time.Duration { return 5 * time.Millisecond },
		},
		chain.StepFunc{StepName: "assemble", Fn: assembleOptimizedDialogue},
		chain.StepFunc{StepName: "print", Fn: printResult},
	)

	st := &chain.State{Query: query}
	if err := ch.Run(ctx, st); err != nil {
		fmt.Printf("chain error: %v\n", err)
		return
	}
}

func tryLLM(ctx context.Context) (llm.LLMHandler, string, bool) {
	useLLM := strings.TrimSpace(utils.GetEnv("USE_LLM"))
	wantLLM := useLLM == "1" || strings.EqualFold(useLLM, "true")
	provider := strings.TrimSpace(utils.GetEnv("LLM_PROVIDER"))
	if !wantLLM && provider == "" {
		return nil, "", false
	}
	switch {
	case provider != "":
	case strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY")) != "":
		provider = llm.ProviderOpenAI
	case strings.TrimSpace(utils.GetEnv("OLLAMA_BASE_URL")) != "":
		provider = llm.ProviderOllama
	default:
		return nil, "", false
	}

	model := strings.TrimSpace(utils.GetEnv("LLM_MODEL"))
	opts := &llm.LLMOptions{
		Provider:     provider,
		ApiKey:       strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY")),
		BaseURL:      strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL")),
		SystemPrompt: "你是对话改写与扩写助手，只输出指令要求的内容。",
	}
	switch provider {
	case llm.ProviderOllama:
		if strings.TrimSpace(opts.BaseURL) == "" {
			opts.BaseURL = "http://localhost:11434/v1"
		}
		if model == "" {
			model = "gemma3:1b"
		}
	case llm.ProviderOpenAI:
		if model == "" {
			model = "gpt-4o-mini"
		}
	default:
		if model == "" {
			model = "gpt-4o-mini"
		}
	}

	h, err := llm.NewProviderHandler(ctx, provider, opts)
	if err != nil {
		fmt.Printf("llm init skipped (%v), using rule rewrite/expand\n", err)
		return nil, "", false
	}
	return h, model, true
}

func assembleOptimizedDialogue(_ context.Context, st *chain.State) error {
	rw := strings.TrimSpace(st.Rewritten)
	if rw == "" {
		rw = st.Query
	}
	ex := strings.TrimSpace(st.Expanded)
	style, _ := st.Meta["dialogue_style"].(string)
	if style == "" {
		style = "default"
	}
	var b strings.Builder
	b.WriteString("【优化后表述】\n")
	b.WriteString(rw)
	b.WriteString("\n\n【扩展关键词】\n")
	if len(st.ExpandTerms) > 0 {
		b.WriteString(strings.Join(st.ExpandTerms, ", "))
	} else {
		b.WriteString(ex)
	}
	b.WriteString("\n\n【路由策略】")
	b.WriteString(style)
	b.WriteString("\n")
	st.Answer = strings.TrimSpace(b.String())
	return nil
}

func printResult(_ context.Context, st *chain.State) error {
	fmt.Printf("--- dialogue optimize chain (no RAG) ---\n")
	fmt.Printf("original_query:\n%s\n\n", st.Query)
	fmt.Printf("rewritten:\n%s\n\n", st.Rewritten)
	fmt.Printf("expanded:\n%s\n\n", st.Expanded)
	fmt.Printf("terms: %v\n\n", st.ExpandTerms)
	fmt.Printf("meta: %+v\n\n", st.Meta)
	fmt.Printf("timings: %v\n\n", st.Timings)
	fmt.Printf("%s\n", st.Answer)
	return nil
}
