package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/utils"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	baseURL := strings.TrimSpace(utils.GetEnv("OLLAMA_BASE_URL"))
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	model := strings.TrimSpace(utils.GetEnv("OLLAMA_MODEL"))
	if model == "" {
		model = "gemma3:1b"
	}
	question := strings.TrimSpace(utils.GetEnv("QUERY"))
	if question == "" {
		question = "请用中文给出3条提高Go服务性能的建议。"
	}

	h, err := llm.NewOllamaHandler(ctx, &llm.LLMOptions{
		ApiKey:       strings.TrimSpace(utils.GetEnv("OLLAMA_API_KEY")),
		BaseURL:      baseURL,
		SystemPrompt: "你是一个简洁的工程助手。",
	})
	if err != nil {
		fmt.Printf("init ollama handler failed: %v\n", err)
		return
	}

	fmt.Printf("provider=%s model=%s baseURL=%s\n", h.Provider(), model, baseURL)

	// 1) non-stream example
	resp, err := h.QueryWithOptions(question, &llm.QueryOptions{
		Model:       model,
		Temperature: float32(0.2),
		MaxTokens:   512,
	})
	if err != nil {
		fmt.Printf("query failed: %v\n", err)
		return
	}
	if resp != nil && len(resp.Choices) > 0 {
		fmt.Printf("\n[non-stream]\n%s\n", strings.TrimSpace(resp.Choices[0].Content))
	}

	// 2) stream example
	fmt.Printf("\n[stream]\n")
	_, err = h.QueryStream("请把上面的建议整理成一个5行以内的执行清单。", &llm.QueryOptions{
		Model:       model,
		Temperature: float32(0.2),
		MaxTokens:   512,
	}, func(segment string, isComplete bool) error {
		if isComplete {
			fmt.Printf("\n")
			return nil
		}
		fmt.Print(segment)
		return nil
	})
	if err != nil {
		fmt.Printf("stream failed: %v\n", err)
		return
	}
}

