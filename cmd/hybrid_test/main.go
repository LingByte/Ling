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
	"github.com/LingByte/Ling/pkg/retrieval"
	"github.com/LingByte/Ling/pkg/utils"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	namespace := strings.TrimSpace(utils.GetEnv("NAMESPACE"))
	if namespace == "" {
		namespace = "default"
	}

	// Parse + chunk
	inputPath := filepath.Join("cmd", "parser_test", "fixtures", "sample.txt")
	parseRes, err := parser.ParsePath(ctx, inputPath, &parser.ParseOptions{MaxTextLength: 20000, PreserveLineBreaks: true})
	if err != nil {
		fmt.Printf("parse failed: %v\n", err)
		return
	}
	chunker, _ := chunk.New(chunk.ChunkerTypeRule, nil)
	chunks, err := chunker.Chunk(ctx, parseRes.Text, &chunk.ChunkOptions{MaxChars: 300, OverlapChars: 40, MinChars: 30})
	if err != nil {
		fmt.Printf("chunk failed: %v\n", err)
		return
	}

	// Build Qdrant knowledge handler
	qdrantURL := utils.GetEnv("QDRANT_URL")
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
	vec, err := knowledge.New(knowledge.KnowledgeQdrant, &knowledge.FactoryOptions{Qdrant: &knowledge.QdrantOptions{BaseURL: qdrantURL, APIKey: utils.GetEnv("QDRANT_API_KEY"), Collection: collection, Embedder: emb}})
	if err != nil {
		fmt.Printf("knowledge init failed: %v\n", err)
		return
	}

	idxBase := utils.GetEnv("KEYWORD_INDEX_BASE")
	if strings.TrimSpace(idxBase) == "" {
		idxBase = filepath.Join(".data", "keyword_index")
	}

	reranker := buildReranker()
	extractor, extractorName := buildExtractor(ctx)
	hy, err := retrieval.NewHybrid(&retrieval.HybridOptions{KnowledgeProvider: vec.Provider(), Vector: vec, Reranker: reranker, IndexBasePath: idxBase})
	if err != nil {
		fmt.Printf("hybrid init failed: %v\n", err)
		return
	}
	defer hy.Close()
	if reranker != nil {
		fmt.Printf("rerank: enabled\n")
	} else {
		fmt.Printf("rerank: disabled\n")
	}
	if extractor != nil {
		fmt.Printf("extractor: %s\n", extractorName)
	} else {
		fmt.Printf("extractor: disabled\n")
	}

	now := time.Now()
	records := make([]knowledge.Record, 0, len(chunks))
	ids := make([]string, 0, len(chunks))
	docID := strings.TrimSuffix(parseRes.FileName, filepath.Ext(parseRes.FileName))
	for _, c := range chunks {
		id := fmt.Sprintf("%s#chunk_%d", docID, c.Index)
		ids = append(ids, id)
		tags := []string{"hybrid_test"}
		meta := map[string]any{"chunk_index": c.Index, "chunk_title": c.Title}
		if extractor != nil {
			x, err := extractor.Extract(ctx, extract.ChunkInput{DocumentTitle: parseRes.FileName, FileName: parseRes.FileName, Source: "hybrid_test", Namespace: namespace, ChunkIndex: c.Index, Text: c.Text}, &extract.Options{MaxTags: 12})
			if err == nil && x != nil {
				if len(x.Tags) > 0 {
					tags = append(tags, x.Tags...)
				}
				for k, v := range x.Metadata {
					meta[k] = v
				}
			}
		}
		records = append(records, knowledge.Record{ID: id, Source: "hybrid_test", Title: parseRes.FileName, Content: c.Text, Tags: tags, Metadata: meta, CreatedAt: now, UpdatedAt: now})
	}
	defer func() {
		_ = hy.Delete(ctx, ids, &knowledge.DeleteOptions{Namespace: namespace})
	}()

	if err := hy.Upsert(ctx, records, &knowledge.UpsertOptions{Namespace: namespace}); err != nil {
		fmt.Printf("upsert failed: %v\n", err)
		return
	}

	q := utils.GetEnv("QUERY")
	if strings.TrimSpace(q) == "" {
		q = "chunk overlap" // a more specific keyword+semantic query
	}

	res, err := hy.QueryHybrid(ctx, q, &knowledge.QueryOptions{Namespace: namespace, TopK: 5})
	if err != nil {
		fmt.Printf("query hybrid failed: %v\n", err)
		return
	}
	fmt.Printf("knowledgeProvider=%s results=%d\n", hy.KnowledgeProvider(), len(res))
	for i, r := range res {
		p := strings.TrimSpace(r.Record.Content)
		if len(p) > 120 {
			p = p[:120] + "..."
		}
		fmt.Printf("  - [%d] score=%.4f id=%s preview=%q\n", i, r.Score, r.Record.ID, p)
		if i >= 4 {
			break
		}
	}
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
