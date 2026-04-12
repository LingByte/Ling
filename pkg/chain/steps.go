package chain

import (
	"context"
	"strings"

	"github.com/LingByte/Ling/pkg/compress"
	"github.com/LingByte/Ling/pkg/expand"
	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/rewrite"
	"github.com/LingByte/Ling/pkg/retrieval"
	"github.com/LingByte/Ling/pkg/selfquery"
)

type RewriteStep struct {
	Rewriter rewrite.Rewriter
	Request  rewrite.RewriteRequest
	// If true, use State.Expanded as rewrite input when non-empty; otherwise State.Query.
	UseExpanded bool
}

func (s RewriteStep) Name() string { return "rewrite" }

func (s RewriteStep) Run(ctx context.Context, st *State) error {
	if s.Rewriter == nil {
		return nil
	}
	req := s.Request
	q := st.Query
	if s.UseExpanded && strings.TrimSpace(st.Expanded) != "" {
		q = st.Expanded
	}
	req.Query = q
	resp, err := s.Rewriter.Rewrite(ctx, req)
	if err != nil {
		return err
	}
	st.Rewritten = strings.TrimSpace(resp.Rewritten)
	if st.Rewritten == "" {
		st.Rewritten = q
	}
	return nil
}

type SelfQueryStep struct {
	Extractor *selfquery.Extractor
	Options   *selfquery.Options
}

func (s SelfQueryStep) Name() string { return "selfquery" }

func (s SelfQueryStep) Run(ctx context.Context, st *State) error {
	if s.Extractor == nil {
		return nil
	}
	res, err := s.Extractor.Extract(ctx, st.Query, s.Options)
	if err != nil {
		return err
	}
	st.SelfQueryText = strings.TrimSpace(res.Query)
	st.SelfQueryFilters = res.Filters
	if st.Meta == nil {
		st.Meta = map[string]any{}
	}
	st.Meta["selfquery_raw"] = res.Raw
	st.Meta["selfquery_spec"] = res.Spec
	return nil
}

type ExpandStep struct {
	Expander expand.Expander
	Request  expand.ExpandRequest
	// If true, use State.Rewritten as input; otherwise use State.Query.
	UseRewritten bool
	// If true, prefer State.SelfQueryText (when set) over Query/Rewritten for expansion input.
	UseSelfQuery bool
}

func (s ExpandStep) Name() string { return "expand" }

func (s ExpandStep) Run(ctx context.Context, st *State) error {
	if s.Expander == nil {
		return nil
	}
	base := st.Query
	if s.UseSelfQuery && strings.TrimSpace(st.SelfQueryText) != "" {
		base = st.SelfQueryText
	} else if s.UseRewritten && strings.TrimSpace(st.Rewritten) != "" {
		base = st.Rewritten
	}
	req := s.Request
	req.Query = base
	resp, err := s.Expander.Expand(ctx, req)
	if err != nil {
		return err
	}
	st.ExpandTerms = resp.Terms
	st.Expanded = strings.TrimSpace(resp.Expanded)
	if st.Expanded == "" {
		st.Expanded = base
	}
	return nil
}

type HybridRetriever interface {
	QueryHybrid(ctx context.Context, query string, options *knowledge.QueryOptions) ([]knowledge.QueryResult, error)
}

type RetrieveStep struct {
	Retriever HybridRetriever
	Options   *knowledge.QueryOptions
	UseExpanded bool
	// If true, use State.SelfQueryText as retrieval query when non-empty (overrides Rewritten/Expanded).
	UseSelfQueryString bool
	// If true, merge State.SelfQueryFilters into Options.Filters for vector leg.
	UseSelfQueryFilters bool
}

func (s RetrieveStep) Name() string { return "retrieve" }

func (s RetrieveStep) Run(ctx context.Context, st *State) error {
	if s.Retriever == nil {
		return nil
	}
	q := st.Query
	switch {
	case s.UseSelfQueryString && strings.TrimSpace(st.SelfQueryText) != "":
		q = strings.TrimSpace(st.SelfQueryText)
	case s.UseExpanded && strings.TrimSpace(st.Expanded) != "":
		q = st.Expanded
	case strings.TrimSpace(st.Rewritten) != "":
		q = st.Rewritten
	}
	opts := s.Options
	if s.UseSelfQueryFilters && len(st.SelfQueryFilters) > 0 {
		var merged knowledge.QueryOptions
		if opts != nil {
			merged = *opts
		}
		merged.Filters = retrieval.MergeQdrantFilters(merged.Filters, st.SelfQueryFilters)
		opts = &merged
	}
	res, err := s.Retriever.QueryHybrid(ctx, q, opts)
	if err != nil {
		return err
	}
	st.Results = res
	return nil
}

type CompressStep struct {
	Compressor compress.Compressor
	Request    compress.CompressRequest
}

func (s CompressStep) Name() string { return "compress" }

func (s CompressStep) Run(ctx context.Context, st *State) error {
	if s.Compressor == nil {
		return nil
	}
	items := make([]compress.Item, 0, len(st.Results))
	for _, r := range st.Results {
		items = append(items, compress.Item{ID: r.Record.ID, Title: r.Record.Title, Content: r.Record.Content, Tags: r.Record.Tags, Metadata: r.Record.Metadata})
	}
	if len(items) == 0 {
		st.Context = ""
		return nil
	}
	req := s.Request
	req.Query = st.Query
	req.Items = items
	resp, err := s.Compressor.Compress(ctx, req)
	if err != nil {
		return err
	}
	st.Context = strings.TrimSpace(resp.Compressed)
	return nil
}

type AnswerStep struct {
	LLM   llm.LLMHandler
	Model string
	// If provided, called to build final prompt.
	BuildPrompt func(st *State) string
	// If true with BuildPrompt set, call the LLM even when no retrieve/compress context exists.
	AllowWithoutRetrieve bool
	// If true, default RAG prompt allows general knowledge when context is empty or clearly
	// insufficient for the question (e.g. creative tasks while KB holds unrelated docs).
	// When false (default), keeps strict "only context / else 未找到相关信息" behavior.
	RelaxContextOnly bool
	// EmotionalTone is passed to llm.QueryOptions so the provider can append a warmer style instruction.
	EmotionalTone bool
}

func (s AnswerStep) Name() string { return "answer" }

func (s AnswerStep) Run(ctx context.Context, st *State) error {
	_ = ctx
	if s.LLM == nil {
		return nil
	}
	noEvidence := strings.TrimSpace(st.Context) == "" && len(st.Results) == 0
	if noEvidence {
		allowLLM := (s.AllowWithoutRetrieve && s.BuildPrompt != nil) || s.RelaxContextOnly
		if !allowLLM {
			st.Answer = "未找到相关信息"
			return nil
		}
	}
	prompt := ""
	if s.BuildPrompt != nil {
		prompt = s.BuildPrompt(st)
	}
	if strings.TrimSpace(prompt) == "" {
		if s.RelaxContextOnly {
			prompt = "你是助手。下面「资料区」来自检索，可能为空、不完整或与用户问题无关。\n\n" +
				"规则：\n" +
				"1) 若资料与用户问题高度相关且足以作答，请主要依据资料回答，可摘取要点。\n" +
				"2) 若资料为空、或与问题明显无关（例如用户要写小说而资料是技术文档）、或资料不足以完成请求，请不要只回复「未找到相关信息」；请直接用你的通用知识完成用户任务，并可用一句话说明知识库未覆盖或资料不相关。\n\n" +
				"资料区：\n" + strings.TrimSpace(st.Context) + "\n\n" +
				"用户问题：" + strings.TrimSpace(st.Query) + "\n\n" +
				"回答："
		} else {
			// Default minimal RAG prompt with strict not-found instruction.
			prompt = "You are a helpful assistant. Answer the user's question using ONLY the provided context.\n\n" +
				"Context:\n" + strings.TrimSpace(st.Context) + "\n\n" +
				"Instruction: 如果提供的资料中找不到答案,直接回回复'未找到相关信息'。禁止使用你自己的知识补充。\n\n" +
				"Question: " + strings.TrimSpace(st.Query) + "\n" +
				"Answer:"
		}
	}
	model := strings.TrimSpace(s.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}
	resp, err := s.LLM.QueryWithOptions(prompt, &llm.QueryOptions{
		Model:         model,
		EmotionalTone: s.EmotionalTone,
	})
	if err != nil {
		return err
	}
	if resp == nil || len(resp.Choices) == 0 {
		st.Answer = ""
		return nil
	}
	st.Answer = strings.TrimSpace(resp.Choices[0].Content)
	return nil
}
