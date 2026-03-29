package chain

import (
	"context"
	"errors"
	"strings"

	"github.com/LingByte/Ling/pkg/censor"
	"github.com/LingByte/Ling/pkg/compress"
	"github.com/LingByte/Ling/pkg/expand"
	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/llm"
	"github.com/LingByte/Ling/pkg/rewrite"
)

type RewriteStep struct {
	Rewriter rewrite.Rewriter
	Request  rewrite.RewriteRequest
}

func (s RewriteStep) Name() string { return "rewrite" }

func (s RewriteStep) Run(ctx context.Context, st *State) error {
	if s.Rewriter == nil {
		return nil
	}
	req := s.Request
	req.Query = st.Query
	resp, err := s.Rewriter.Rewrite(ctx, req)
	if err != nil {
		return err
	}
	st.Rewritten = strings.TrimSpace(resp.Rewritten)
	if st.Rewritten == "" {
		st.Rewritten = st.Query
	}
	return nil
}

type ExpandStep struct {
	Expander expand.Expander
	Request  expand.ExpandRequest
	// If true, use State.Rewritten as input; otherwise use State.Query.
	UseRewritten bool
}

func (s ExpandStep) Name() string { return "expand" }

func (s ExpandStep) Run(ctx context.Context, st *State) error {
	if s.Expander == nil {
		return nil
	}
	base := st.Query
	if s.UseRewritten && strings.TrimSpace(st.Rewritten) != "" {
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
}

func (s RetrieveStep) Name() string { return "retrieve" }

func (s RetrieveStep) Run(ctx context.Context, st *State) error {
	if s.Retriever == nil {
		return nil
	}
	q := st.Query
	if strings.TrimSpace(st.Rewritten) != "" {
		q = st.Rewritten
	}
	if s.UseExpanded && strings.TrimSpace(st.Expanded) != "" {
		q = st.Expanded
	}
	res, err := s.Retriever.QueryHybrid(ctx, q, s.Options)
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

type CensorStep struct {
	Censor censor.Censor
	Request censor.AssessRequest
	// If empty, default to censoring State.Query.
	Target string
}

func (s CensorStep) Name() string { return "censor" }

func (s CensorStep) Run(ctx context.Context, st *State) error {
	if s.Censor == nil {
		return nil
	}
	text := s.Target
	if strings.TrimSpace(text) == "" {
		text = st.Query
	}
	req := s.Request
	req.Text = text
	resp, err := s.Censor.Assess(ctx, req)
	if resp != nil {
		st.Meta["censor_action"] = resp.Action
		st.Meta["censor_matches"] = resp.Matches
		if resp.Redacted {
			st.Meta["censor_processed"] = resp.Processed
		}
	}
	if errors.Is(err, censor.ErrBlocked) {
		st.Blocked = true
		return ErrStop
	}
	return err
}

type AnswerStep struct {
	LLM   llm.LLMHandler
	Model string
	// If provided, called to build final prompt.
	BuildPrompt func(st *State) string
}

func (s AnswerStep) Name() string { return "answer" }

func (s AnswerStep) Run(ctx context.Context, st *State) error {
	_ = ctx
	if s.LLM == nil {
		return nil
	}
	// Hard fallback: if there is no knowledge context, do not call LLM.
	if strings.TrimSpace(st.Context) == "" && len(st.Results) == 0 {
		st.Answer = "未找到相关信息"
		return nil
	}
	prompt := ""
	if s.BuildPrompt != nil {
		prompt = s.BuildPrompt(st)
	}
	if strings.TrimSpace(prompt) == "" {
		// Default minimal RAG prompt with strict not-found instruction.
		prompt = "You are a helpful assistant. Answer the user's question using ONLY the provided context.\n\n" +
			"Context:\n" + strings.TrimSpace(st.Context) + "\n\n" +
			"Instruction: 如果提供的资料中找不到答案,直接回回复'未找到相关信息'。禁止使用你自己的知识补充。\n\n" +
			"Question: " + strings.TrimSpace(st.Query) + "\n" +
			"Answer:"
	}
	model := strings.TrimSpace(s.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}
	out, err := s.LLM.Query(prompt, model)
	if err != nil {
		return err
	}
	st.Answer = strings.TrimSpace(out)
	return nil
}
