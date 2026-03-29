package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/agent/exec"
	"github.com/LingByte/Ling/pkg/agent/plan"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/utils"
)

type llmAdapter struct{ h llm.LLMHandler }

func (a *llmAdapter) Query(text, model string) (string, error) { return a.h.Query(text, model) }

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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
		goal = "基于知识库回答：如何提高Python代码效率？"
	}

	maxTasks := 6

	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{ApiKey: apiKey, BaseURL: baseURL, SystemPrompt: "你是任务拆分与执行系统。"})
	if err != nil {
		fmt.Printf("llm init failed: %v\n", err)
		return
	}

	dec := &plan.LLMDecomposer{LLM: &llmAdapter{h: h}, Model: model, MaxTasks: maxTasks}
	pl, err := dec.Decompose(ctx, plan.Request{Goal: goal, LLMModel: model, MaxTasks: maxTasks})
	if err != nil {
		fmt.Printf("decompose failed: %v\n", err)
		return
	}

	fmt.Printf("goal=%q tasks=%d\n", pl.Goal, len(pl.Tasks))
	for i, t := range pl.Tasks {
		exp := strings.TrimSpace(t.Expected)
		if exp != "" {
			fmt.Printf("  - [%d] id=%s title=%q depends=%v expected=%q\n", i, t.ID, t.Title, t.DependsOn, exp)
			fmt.Printf("        instruction=%q\n", t.Instruction)
		} else {
			fmt.Printf("  - [%d] id=%s title=%q depends=%v instruction=%q\n", i, t.ID, t.Title, t.DependsOn, t.Instruction)
		}
	}

	executor := &exec.Executor{
		Runner:    &exec.LLMTaskRunner{LLM: h, Model: model},
		Evaluator: &exec.LLMTaskEvaluator{LLM: h, Model: model},
		Opts:      exec.Options{StopOnError: true, MaxTasks: 32, MaxAttempts: 3},
	}
	res, err := executor.Run(ctx, pl)
	if err != nil {
		fmt.Printf("execute failed: %v\n", err)
	}

	fmt.Printf("\nExecution results:\n")
	for _, tr := range res.TaskResults {
		fmt.Printf("- task=%s status=%s attempts=%d latency=%s\n", tr.TaskID, tr.Status, tr.Attempts, tr.Latency)
		if strings.TrimSpace(tr.Error) != "" {
			fmt.Printf("  error=%s\n", tr.Error)
		}
		if strings.TrimSpace(tr.Feedback) != "" {
			fmt.Printf("  feedback=%q\n", strings.TrimSpace(tr.Feedback))
		}
		if strings.TrimSpace(tr.Output) != "" {
			out := strings.TrimSpace(tr.Output)
			if len(out) > 200 {
				out = out[:200] + "..."
			}
			fmt.Printf("  output=%q\n", out)
		}
	}

	// Final: last successful task output (best-effort)
	final := ""
	for i := len(res.TaskResults) - 1; i >= 0; i-- {
		if res.TaskResults[i].Status == exec.TaskSucceeded {
			final = res.TaskResults[i].Output
			break
		}
	}
	if strings.TrimSpace(final) != "" {
		fmt.Printf("\nFinal output:\n%s\n", strings.TrimSpace(final))
	}
}
