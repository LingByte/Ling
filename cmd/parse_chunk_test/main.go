package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/LingByte/Ling/pkg/chunk"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/parser"
	"github.com/LingByte/Ling/pkg/utils"
)

func main() {
	ctx := context.Background()
	fixtures := filepath.Join("cmd", "parser_test", "fixtures")

	entries, err := os.ReadDir(fixtures)
	if err != nil {
		fmt.Printf("failed to read fixtures dir %s: %v\n", fixtures, err)
		os.Exit(1)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	fmt.Printf("Parse -> Chunk integration test runner\n")
	fmt.Printf("Fixtures: %s\n\n", fixtures)
	fmt.Printf("Env: CHUNKER=%q OPENAI_BASE_URL_set=%v OPENAI_MODEL=%q\n\n",
		utils.GetEnv("CHUNKER"),
		strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL")) != "",
		utils.GetEnv("OPENAI_MODEL"),
	)

	parseOpts := &parser.ParseOptions{MaxTextLength: 5000, PreserveLineBreaks: true}
	chunkOpts := &chunk.ChunkOptions{MaxChars: 300, OverlapChars: 40, MinChars: 30}

	chunker, chunkerName := buildChunker(ctx)

	for _, name := range names {
		path := filepath.Join(fixtures, name)
		fmt.Printf("==> %s\n", name)

		res, err := parser.ParsePath(ctx, path, parseOpts)
		if err != nil {
			fmt.Printf("parse failed: %v\n\n", err)
			continue
		}

		chunks, err := chunker.Chunk(ctx, res.Text, chunkOpts)
		if err != nil {
			fmt.Printf("chunk failed: %v\n\n", err)
			continue
		}

		fmt.Printf("parsed: type=%s chars=%d sections=%d\n", res.FileType, len(res.Text), len(res.Sections))
		fmt.Printf("chunks: provider=%s (%s) count=%d (maxChars=%d overlap=%d)\n", chunker.Provider(), chunkerName, len(chunks), chunkOpts.MaxChars, chunkOpts.OverlapChars)

		for i, c := range chunks {
			preview := strings.TrimSpace(c.Text)
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			fmt.Printf("  - chunk[%d] chars=%d preview=%q\n", i, len(c.Text), preview)
			if i >= 4 {
				if len(chunks) > 5 {
					fmt.Printf("  ... (%d more chunks)\n", len(chunks)-5)
				}
				break
			}
		}
		fmt.Println()
	}
}

func buildChunker(ctx context.Context) (chunk.Chunker, string) {
	mode := strings.ToLower(strings.TrimSpace(utils.GetEnv("CHUNKER")))
	if mode == "" {
		mode = chunk.ChunkerTypeRule
	}

	if mode != chunk.ChunkerTypeLLM {
		c, _ := chunk.New(mode, nil)
		return c, "default"
	}

	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		fmt.Printf("CHUNKER=llm requested but missing OPENAI_API_KEY/OPENAI_BASE_URL/OPENAI_MODEL; falling back to rule chunker\n\n")
		c, _ := chunk.New(chunk.ChunkerTypeRule, nil)
		return c, "fallback"
	}

	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{ApiKey: apiKey, BaseURL: baseURL})
	if err != nil {
		fmt.Printf("failed to create OpenAI handler: %v; falling back to rule chunker\n\n", err)
		c, _ := chunk.New(chunk.ChunkerTypeRule, nil)
		return c, "fallback"
	}

	c, err := chunk.New(chunk.ChunkerTypeLLM, &chunk.FactoryOptions{LLM: h, Model: model})
	if err != nil {
		fmt.Printf("failed to create LLM chunker: %v; falling back to rule chunker\n\n", err)
		c2, _ := chunk.New(chunk.ChunkerTypeRule, nil)
		return c2, "fallback"
	}
	return c, "openai"
}
