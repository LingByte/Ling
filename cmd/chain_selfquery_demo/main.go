// chain_selfquery_demo 演示链式流程：
//
//	自查询 → 查询扩展 → 查询重写 → 混合检索(RAG，向量+关键词，内置 rerank) → 规则压缩上下文 → LLM 依据片段作答
//
// 环境变量 — LLM（与 pkg/llm 一致，utils.GetEnv 可读 .env）：
//
//	OPENAI_API_KEY     必填（或当前 Provider 对应密钥）
//	OPENAI_BASE_URL    OpenAI 兼容网关（如阿里云 compatible-mode/v1）
//	LING_LLM_PROVIDER  可选
//	OPENAI_MODEL       可选
//
// 环境变量 — RAG（默认开启；设置 LING_SKIP_RAG=1 则跳过检索，仅跑前半段 + 无检索回答）：
//
//	QDRANT_URL / QDRANT_API_KEY / QDRANT_COLLECTION
//	NVIDIA_EMBEDDINGS_URL / NVIDIA_EMBEDDINGS_MODEL / NVIDIA_API_KEY（或你已有的 OpenAI 兼容 embeddings 端点）
//	LING_HYBRID_INDEX_BASE  必填（非 skip 时）：本地关键词索引目录，例如 ./.ling_hybrid_idx
//	SILICONFLOW_RERANK_URL / SILICONFLOW_RERANK_MODEL / SILICONFLOW_API_KEY  可选；不设则 Hybrid 不调用 rerank
//	LING_KB_NAMESPACE       可选，知识库 namespace，默认 default
//	LING_STRICT_RAG=1      可选：答案严格仅依据检索资料，找不到则「未找到相关信息」（默认关闭，允许资料无关时用模型常识/创作完成请求）
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/LingByte/Ling/pkg/chain"
	"github.com/LingByte/Ling/pkg/compress"
	"github.com/LingByte/Ling/pkg/constants"
	"github.com/LingByte/Ling/pkg/expand"
	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/retrieval"
	"github.com/LingByte/Ling/pkg/rewrite"
	"github.com/LingByte/Ling/pkg/selfquery"
	"github.com/LingByte/Ling/pkg/utils"
	"go.uber.org/zap"
)

func main() {
	zap.ReplaceGlobals(zap.NewNop())

	args := os.Args[1:]
	skipRAG := false
	var pos []string
	for _, a := range args {
		if a == "--no-rag" || a == "-no-rag" {
			skipRAG = true
			continue
		}
		pos = append(pos, a)
	}
	question := strings.TrimSpace(strings.Join(pos, " "))
	if question == "" {
		log.Fatal("用法: go run ./cmd/chain_selfquery_demo [--no-rag] <用户问题>")
	}

	apiKey := strings.TrimSpace(utils.GetEnv("OPENAI_API_KEY"))
	if apiKey == "" {
		log.Fatal("需要环境变量 OPENAI_API_KEY（或当前 Provider 对应密钥）")
	}

	ctx := context.Background()
	provider := strings.TrimSpace(utils.GetEnv("LING_LLM_PROVIDER"))
	model := strings.TrimSpace(utils.GetEnv("OPENAI_MODEL"))
	if model == "" {
		model = constants.DefaultModel
	}

	baseURL := strings.TrimSpace(utils.GetEnv("OPENAI_BASE_URL"))
	if baseURL == "" && (provider == "" || strings.EqualFold(provider, llm.ProviderOpenAI)) {
		log.Fatal("使用 OpenAI 兼容接口时必须设置 OPENAI_BASE_URL（可写入 .env，键名 OPENAI_BASE_URL）")
	}

	handler, err := llm.NewProviderHandler(ctx, provider, &llm.LLMOptions{
		Provider: provider,
		ApiKey:   apiKey,
		BaseURL:  baseURL,
		Logger:   zap.NewNop(),
	})
	if err != nil {
		log.Fatalf("创建 LLM handler: %v", err)
	}

	expander, err := expand.New(expand.ModeLLM, &expand.FactoryOptions{LLM: handler})
	if err != nil {
		log.Fatalf("创建 expander: %v", err)
	}

	rewriter, err := rewrite.New(rewrite.ModeLLM, &rewrite.FactoryOptions{
		LLM:      handler,
		LLMModel: model,
	})
	if err != nil {
		log.Fatalf("创建 rewriter: %v", err)
	}

	allowed := []string{"source", "doc_type", "location", "years", "dates", "tags_any"}
	extractor := selfquery.NewExtractor(handler, allowed)

	var hybrid *retrieval.HybridHandler
	if !skipRAG && strings.TrimSpace(utils.GetEnv("LING_SKIP_RAG")) != "1" {
		hybrid, err = buildHybrid()
		if err != nil {
			log.Fatalf("初始化 Hybrid RAG: %v\n（若暂无 Qdrant/向量服务，可加 --no-rag 或设置 LING_SKIP_RAG=1）", err)
		}
	}

	ruleComp, err := compress.New(compress.ModeRule, nil)
	if err != nil {
		log.Fatalf("创建压缩器: %v", err)
	}

	ns := strings.TrimSpace(utils.GetEnv("LING_KB_NAMESPACE"))
	if ns == "" {
		ns = "default"
	}

	steps := []chain.Step{
		chain.SelfQueryStep{
			Extractor: extractor,
			Options:   &selfquery.Options{Model: model, AllowedFields: allowed},
		},
		chain.ExpandStep{
			Expander: expander,
			Request: expand.ExpandRequest{
				Mode:      expand.ModeLLM,
				MaxTerms:  10,
				Separator: " ",
				LLMModel:  model,
			},
			UseSelfQuery: true,
		},
		chain.RewriteStep{
			Rewriter: rewriter,
			Request: rewrite.RewriteRequest{
				Mode:     rewrite.ModeLLM,
				LLMModel: model,
				MaxChars: 400,
			},
			UseExpanded: true,
		},
	}

	if hybrid != nil {
		steps = append(steps,
			chain.RetrieveStep{
				Retriever:           hybrid,
				Options:             &knowledge.QueryOptions{Namespace: ns, TopK: 8},
				UseSelfQueryString:  true,
				UseSelfQueryFilters: true,
			},
			chain.CompressStep{
				Compressor: ruleComp,
				Request:    compress.CompressRequest{MaxChars: 6000},
			},
			chain.AnswerStep{
				LLM:              handler,
				Model:            model,
				RelaxContextOnly: strings.TrimSpace(utils.GetEnv("LING_STRICT_RAG")) != "1",
			},
		)
	} else {
		steps = append(steps, chain.AnswerStep{
			LLM:                  handler,
			Model:                model,
			AllowWithoutRetrieve: true,
			BuildPrompt: func(st *chain.State) string {
				return "你是助手。根据下面「重写后的检索式」理解用户意图，直接回答「用户原始问题」。\n\n" +
					"用户原始问题：" + strings.TrimSpace(st.Query) + "\n\n" +
					"重写后的检索式：" + strings.TrimSpace(st.Rewritten) + "\n\n" +
					"回答（简洁）："
			},
		})
	}

	c := chain.New(steps...)
	st := &chain.State{Query: question}
	if err := c.Run(ctx, st); err != nil {
		log.Fatalf("chain 执行失败: %v", err)
	}

	fmt.Println("=== Self-Query 抽取 query ===")
	fmt.Println(st.SelfQueryText)
	if b, e := json.MarshalIndent(st.SelfQueryFilters, "", "  "); e == nil && len(st.SelfQueryFilters) > 0 {
		fmt.Println("=== Self-Query filters ===")
		fmt.Println(string(b))
	}
	fmt.Println("=== 查询扩展 Expanded ===")
	fmt.Println(st.Expanded)
	if len(st.ExpandTerms) > 0 {
		fmt.Println("=== 扩展词 ===")
		fmt.Println(strings.Join(st.ExpandTerms, ", "))
	}
	fmt.Println("=== 查询重写 Rewritten ===")
	fmt.Println(st.Rewritten)
	if hybrid != nil {
		fmt.Println("=== RAG 命中（Hybrid 已按需 rerank）===")
		for i, r := range st.Results {
			flag := ""
			if r.Record.Metadata != nil {
				if v, ok := r.Record.Metadata["reranked"].(bool); ok && v {
					flag = " [reranked]"
				}
			}
			preview := r.Record.Content
			if len(preview) > 160 {
				preview = preview[:160] + "…"
			}
			fmt.Printf("  [%d] score=%.4f id=%s title=%q%s\n%s\n", i+1, r.Score, r.Record.ID, r.Record.Title, flag, preview)
		}
		fmt.Println("=== 压缩后上下文（片段摘要）===")
		fmt.Println(st.Context)
	}
	fmt.Println("=== LLM 输出 ===")
	fmt.Println(st.Answer)
	if len(st.Timings) > 0 {
		fmt.Println("=== 各步耗时 ===")
		for k, v := range st.Timings {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
}

func buildHybrid() (*retrieval.HybridHandler, error) {
	qURL := strings.TrimSpace(utils.GetEnv("QDRANT_URL"))
	qKey := strings.TrimSpace(utils.GetEnv("QDRANT_API_KEY"))
	col := strings.TrimSpace(utils.GetEnv("QDRANT_COLLECTION"))
	idxBase := strings.TrimSpace(utils.GetEnv("LING_HYBRID_INDEX_BASE"))
	if qURL == "" || col == "" {
		return nil, fmt.Errorf("需要 QDRANT_URL 与 QDRANT_COLLECTION")
	}
	if idxBase == "" {
		return nil, fmt.Errorf("需要 LING_HYBRID_INDEX_BASE（本地关键词索引根目录）")
	}

	nvURL := strings.TrimSpace(utils.GetEnv("NVIDIA_EMBEDDINGS_URL"))
	nvModel := strings.TrimSpace(utils.GetEnv("NVIDIA_EMBEDDINGS_MODEL"))
	nvKey := strings.TrimSpace(utils.GetEnv("NVIDIA_API_KEY"))
	if nvURL == "" || nvModel == "" || nvKey == "" {
		return nil, fmt.Errorf("需要 NVIDIA_EMBEDDINGS_URL / NVIDIA_EMBEDDINGS_MODEL / NVIDIA_API_KEY")
	}

	emb := &knowledge.NvidiaEmbedClient{BaseURL: nvURL, APIKey: nvKey, Model: nvModel}
	qh, err := knowledge.New("qdrant", &knowledge.FactoryOptions{
		Qdrant: &knowledge.QdrantOptions{
			BaseURL:    qURL,
			APIKey:     qKey,
			Collection: col,
			Embedder:   emb,
		},
	})
	if err != nil {
		return nil, err
	}

	var rer retrieval.Reranker
	rURL := strings.TrimSpace(utils.GetEnv("SILICONFLOW_RERANK_URL"))
	rModel := strings.TrimSpace(utils.GetEnv("SILICONFLOW_RERANK_MODEL"))
	rKey := strings.TrimSpace(utils.GetEnv("SILICONFLOW_API_KEY"))
	if rURL != "" && rModel != "" && rKey != "" {
		rer = &knowledge.SiliconFlowRerankClient{BaseURL: rURL, APIKey: rKey, Model: rModel}
	}

	if err := os.MkdirAll(idxBase, 0o755); err != nil {
		return nil, err
	}
	absIdx, err := filepath.Abs(idxBase)
	if err != nil {
		return nil, err
	}

	return retrieval.NewHybrid(&retrieval.HybridOptions{
		KnowledgeProvider: qh.Provider(),
		Vector:            qh,
		Reranker:          rer,
		IndexBasePath:     absIdx,
		VectorTopK:        24,
		KeywordTopK:       24,
		RerankTopN:        16,
	})
}
