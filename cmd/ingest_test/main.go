package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/LingByte/Ling/pkg/chunk"
	"github.com/LingByte/Ling/pkg/extract"
	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/parser"
	"github.com/LingByte/Ling/pkg/utils"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// 1) Parse input file
	inputPath := filepath.Join("cmd", "parser_test", "fixtures", "sample.txt")
	parseRes, err := parser.ParsePath(ctx, inputPath, &parser.ParseOptions{MaxTextLength: 20000, PreserveLineBreaks: true})
	if err != nil {
		fmt.Printf("parse failed: %v\n", err)
		return
	}
	fmt.Printf("parsed: type=%s chars=%d sections=%d file=%s\n", parseRes.FileType, len(parseRes.Text), len(parseRes.Sections), parseRes.FileName)

	// 2) Chunk
	chunker, chunkerName, err := buildChunker(ctx)
	if err != nil {
		fmt.Printf("chunker init failed: %v\n", err)
		return
	}
	chunks, err := chunker.Chunk(ctx, parseRes.Text, &chunk.ChunkOptions{MaxChars: 300, OverlapChars: 40, MinChars: 30, DocumentTitle: parseRes.FileName})
	if err != nil {
		fmt.Printf("chunk failed: %v\n", err)
		return
	}
	fmt.Printf("chunked: provider=%s (%s) count=%d\n", chunker.Provider(), chunkerName, len(chunks))

	// 3) Build Knowledge handler (Qdrant) via factory
	qdrantURL := utils.GetEnv("QDRANT_URL")
	qdrantKey := utils.GetEnv("QDRANT_API_KEY")
	collection := utils.GetEnv("QDRANT_COLLECTION")
	if qdrantURL == "" || collection == "" {
		fmt.Printf("missing QDRANT_URL/QDRANT_COLLECTION\n")
		return
	}

	nvKey := utils.GetEnv("NVIDIA_API_KEY")
	nvURL := utils.GetEnv("NVIDIA_EMBEDDINGS_URL")
	nvModel := utils.GetEnv("NVIDIA_EMBEDDINGS_MODEL")
	if nvKey == "" || nvURL == "" || nvModel == "" {
		fmt.Printf("missing NVIDIA_API_KEY/NVIDIA_EMBEDDINGS_URL/NVIDIA_EMBEDDINGS_MODEL\n")
		return
	}

	emb := &knowledge.NvidiaEmbedClient{BaseURL: nvURL, APIKey: nvKey, Model: nvModel}
	h, err := knowledge.New(knowledge.KnowledgeQdrant, &knowledge.FactoryOptions{
		Qdrant: &knowledge.QdrantOptions{BaseURL: qdrantURL, APIKey: qdrantKey, Collection: collection, Embedder: emb},
	})
	if err != nil {
		fmt.Printf("knowledge factory failed: %v\n", err)
		return
	}

	// 4) Upsert chunks
	now := time.Now()
	records := make([]knowledge.Record, 0, len(chunks))
	ids := make([]string, 0, len(chunks))
	docID := strings.TrimSuffix(parseRes.FileName, filepath.Ext(parseRes.FileName))

	extractor, extractorName := buildExtractor(ctx)
	if extractor == nil {
		fmt.Printf("extractor: disabled\n")
	} else {
		fmt.Printf("extractor: %s\n", extractorName)
	}

	for _, c := range chunks {
		id := fmt.Sprintf("%s#chunk_%d", docID, c.Index)
		ids = append(ids, id)
		tags := []string{"ingest_test"}
		meta := map[string]any{"chunk_index": c.Index, "chunk_title": c.Title}
		if extractor != nil {
			x, err := extractor.Extract(ctx, extract.ChunkInput{DocumentTitle: parseRes.FileName, FileName: parseRes.FileName, Source: "ingest_test", Namespace: "", ChunkIndex: c.Index, Text: c.Text}, &extract.Options{MaxTags: 12})
			if err == nil && x != nil {
				if len(x.Tags) > 0 {
					tags = append(tags, x.Tags...)
				}
				for k, v := range x.Metadata {
					meta[k] = v
				}
			}
		}
		records = append(records, knowledge.Record{
			ID:        id,
			Source:    "ingest_test",
			Title:     parseRes.FileName,
			Content:   c.Text,
			Tags:      tags,
			Metadata:  meta,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	defer func() {
		if err := h.Delete(ctx, ids, &knowledge.DeleteOptions{}); err != nil {
			fmt.Printf("cleanup delete failed: %v\n", err)
			return
		}
		fmt.Printf("cleanup ok: deleted=%d\n", len(ids))
	}()

	if err := h.Upsert(ctx, records, &knowledge.UpsertOptions{}); err != nil {
		fmt.Printf("upsert failed: %v\n", err)
		return
	}
	fmt.Printf("upsert ok: records=%d\n", len(records))

	// 5) Query
	query := utils.GetEnv("INGEST_TEST_QUERY")
	if strings.TrimSpace(query) == "" {
		query = "What is this document about?"
	}
	results, err := h.Query(ctx, query, &knowledge.QueryOptions{TopK: 5})
	if err != nil {
		fmt.Printf("query failed: %v\n", err)
		return
	}
	fmt.Printf("query ok: results=%d\n", len(results))
	for i, r := range results {
		preview := strings.TrimSpace(r.Record.Content)
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		fmt.Printf("  - [%d] score=%.4f id=%s preview=%q\n", i, r.Score, r.Record.ID, preview)
		if i >= 4 {
			break
		}
	}

	// 6) RAG-style LLM call (optional)
	answer, err := answerWithRetrievedKnowledge(ctx, query, results)
	if err != nil {
		fmt.Printf("llm answer skipped/failed: %v\n", err)
		return
	}
	fmt.Printf("\nLLM answer:\n%s\n", strings.TrimSpace(answer))
}

func buildChunker(ctx context.Context) (chunk.Chunker, string, error) {
	mode := strings.ToLower(strings.TrimSpace(utils.GetEnv("CHUNKER")))
	if mode == "" {
		mode = chunk.ChunkerTypeRule
	}

	if mode != chunk.ChunkerTypeLLM {
		c, err := chunk.New(mode, nil)
		return c, "default", err
	}

	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		c, _ := chunk.New(chunk.ChunkerTypeRule, nil)
		return c, "fallback", nil
	}

	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{ApiKey: apiKey, BaseURL: baseURL})
	if err != nil {
		c, _ := chunk.New(chunk.ChunkerTypeRule, nil)
		return c, "fallback", nil
	}

	c, err := chunk.New(chunk.ChunkerTypeLLM, &chunk.FactoryOptions{LLM: h, Model: model})
	if err != nil {
		c2, _ := chunk.New(chunk.ChunkerTypeRule, nil)
		return c2, "fallback", nil
	}
	return c, "openai", nil
}

func buildExtractor(ctx context.Context) (extract.Extractor, string) {
	mode := strings.ToLower(strings.TrimSpace(utils.GetEnv("EXTRACTOR")))
	if mode == "" {
		mode = extract.ExtractorRule
	}
	if mode == "off" || mode == "none" {
		return nil, "off"
	}
	if mode != extract.ExtractorLLM {
		e, err := extract.New(mode, nil)
		if err != nil {
			return nil, "off"
		}
		return e, mode
	}

	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		return nil, "off"
	}
	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{ApiKey: apiKey, BaseURL: baseURL})
	if err != nil {
		return nil, "off"
	}
	e, err := extract.New(extract.ExtractorLLM, &extract.FactoryOptions{LLM: h, Model: model})
	if err != nil {
		return nil, "off"
	}
	return e, "llm"
}

func answerWithRetrievedKnowledge(ctx context.Context, question string, results []knowledge.QueryResult) (string, error) {
	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if apiKey == "" || baseURL == "" || model == "" {
		return "", fmt.Errorf("missing OPENAI_API_KEY/OPENAI_BASE_URL/OPENAI_MODEL")
	}

	h, err := llm.NewOpenaiHandler(ctx, &llm.LLMOptions{ApiKey: apiKey, BaseURL: baseURL})
	if err != nil {
		return "", err
	}

	ctxText := buildContextFromResults(results)
	prompt := "You are a helpful assistant. Answer the user's question using ONLY the provided context.\n\n" +
		"Context:\n" + ctxText + "\n\n" +
		"Instruction: 如果提供的资料中找不到答案,直接回回复'未找到相关信息'。禁止使用你自己的知识补充。\n\n" +
		"Question: " + question + "\n" +
		"Answer:"

	resp, err := h.QueryWithOptions(prompt, &llm.QueryOptions{Model: model, Temperature: float32(0.2)})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty llm response")
	}
	return resp.Choices[0].Content, nil
}

func buildContextFromResults(results []knowledge.QueryResult) string {
	b := strings.Builder{}
	for i, r := range results {
		b.WriteString("[" + fmt.Sprintf("%d", i) + "] ")
		b.WriteString("id=")
		b.WriteString(r.Record.ID)
		b.WriteString(" score=")
		b.WriteString(fmt.Sprintf("%.4f", r.Score))
		b.WriteString("\n")
		b.WriteString(r.Record.Content)
		b.WriteString("\n\n")
		if b.Len() > 6000 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}
