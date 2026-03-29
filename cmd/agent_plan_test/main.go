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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		fmt.Printf("missing OPENAI_API_KEY/OPENAI_BASE_URL/OPENAI_MODEL\n")
		return
	}

	goal := strings.TrimSpace(utils.GetEnv("GOAL"))
	if goal == "" {
		goal = "回答: 如何提高Python代码效率？"
	}

	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{ApiKey: apiKey, BaseURL: baseURL, SystemPrompt: "你是任务规划器。"})
	if err != nil {
		fmt.Printf("llm init failed: %v\n", err)
		return
	}

	dec := &plan.LLMDecomposer{LLM: &llmAdapter{h: h}, Model: model, MaxTasks: 6}

	pl, err := dec.Decompose(ctx, plan.Request{Goal: goal, LLMModel: model, MaxTasks: 6})
	if err != nil {
		fmt.Printf("plan failed: %v\n", err)
		return
	}

	fmt.Printf("goal=%q\n", pl.Goal)
	fmt.Printf("by=%s tasks=%d\n", pl.By, len(pl.Tasks))
	for i, t := range pl.Tasks {
		fmt.Printf("  - [%d] id=%s title=%q depends=%v parallel=%v instruction=%q input=%v\n", i, t.ID, t.Title, t.DependsOn, t.CanParallel, t.Instruction, t.Input)
	}
}
