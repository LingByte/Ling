package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/agent/plan"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/utils"
)

type llmAdapter struct{ h llm.LLMHandler }

func (a *llmAdapter) Query(text, model string) (string, error) { return a.h.Query(text, model) }

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		fmt.Printf("missing OPENAI_API_KEY/OPENAI_BASE_URL/OPENAI_MODEL\n")
		return
	}

	simpleGoal := strings.TrimSpace(utils.GetEnv("SIMPLE_GOAL"))
	if simpleGoal == "" {
		simpleGoal = "把下面这句话翻译成英文：今天北京天气不错。"
	}
	complexGoal := strings.TrimSpace(utils.GetEnv("COMPLEX_GOAL"))
	if complexGoal == "" {
		complexGoal = "基于知识库回答：写一份‘Python 代码性能优化’指南，要求包含：循环优化、内存管理、性能分析方法、以及一个可执行的优化步骤清单。"
	}

	maxTasks := 8
	dec := &plan.LLMDecomposer{LLM: &llmAdapter{h: mustLLM(ctx, apiKey, baseURL)}, Model: model, MaxTasks: maxTasks}

	run := func(title, goal string) {
		fmt.Printf("\n=== %s ===\n", title)
		fmt.Printf("goal=%q\n", goal)
		p, err := dec.Decompose(ctx, plan.Request{Goal: goal, LLMModel: model, MaxTasks: maxTasks})
		if err != nil {
			fmt.Printf("decompose failed: %v\n", err)
			return
		}
		fmt.Printf("by=%s tasks=%d\n", p.By, len(p.Tasks))
		for i, t := range p.Tasks {
			fmt.Printf("  - [%d] id=%s title=%q depends=%v parallel=%v\n", i, t.ID, t.Title, t.DependsOn, t.CanParallel)
			fmt.Printf("        instruction=%q\n", t.Instruction)
			if len(t.Input) > 0 {
				fmt.Printf("        input=%v\n", t.Input)
			}
		}
	}

	run("SIMPLE", simpleGoal)
	run("COMPLEX", complexGoal)
}

func mustLLM(ctx context.Context, apiKey, baseURL string) llm.LLMHandler {
	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{ApiKey: apiKey, BaseURL: baseURL, SystemPrompt: "你是任务拆分器，只负责输出任务拆分 JSON，不要输出多余解释。"})
	if err != nil {
		panic(err)
	}
	return h
}
