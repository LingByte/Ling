package main

import (
	"context"
	"fmt"
	"time"

	"github.com/LingByte/Ling/pkg/chain"
	"github.com/LingByte/Ling/pkg/compress"
	"github.com/LingByte/Ling/pkg/expand"
	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/rewrite"
)

type stubRetriever struct{}

func (r *stubRetriever) QueryHybrid(ctx context.Context, query string, options *knowledge.QueryOptions) ([]knowledge.QueryResult, error) {
	_ = ctx
	_ = options
	return []knowledge.QueryResult{{
		Record: knowledge.Record{ID: "doc#1", Title: "doc1", Content: "Python 循环效率可以通过列表推导式与内置函数提升。内存管理可以用生成器减少峰值。", Tags: []string{"python", "性能优化"}},
		Score:  0.9,
	}}, nil
}

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }
func (f *fakeLLM) Provider() string                         { return "fake" }
func (f *fakeLLM) ResetMemory()                             {}
func (f *fakeLLM) SummarizeMemory(model string) (string, error) { return "", nil }
func (f *fakeLLM) SetMaxMemoryMessages(n int)               {}
func (f *fakeLLM) GetMaxMemoryMessages() int                { return 0 }

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rw, _ := rewrite.New(rewrite.ModeRule, nil)
	exp, _ := expand.New(expand.ModeRule, &expand.FactoryOptions{Synonyms: map[string][]string{"优化": {"性能", "效率"}}})
	cmp, _ := compress.New(compress.ModeRule, nil)

	retr := &stubRetriever{}

	c := chain.New(
		chain.RewriteStep{Rewriter: rw, Request: rewrite.RewriteRequest{MaxChars: 120}},
		chain.ExpandStep{Expander: exp, Request: expand.ExpandRequest{MaxTerms: 6, Separator: " "}, UseRewritten: true},
		chain.RetrieveStep{Retriever: retr, Options: &knowledge.QueryOptions{TopK: 3}, UseExpanded: true},
		chain.CompressStep{Compressor: cmp, Request: compress.CompressRequest{MaxChars: 400}},
		chain.StepFunc{StepName: "print", Fn: func(ctx context.Context, s *chain.State) error {
			_ = ctx
			fmt.Printf("query=%q\n", s.Query)
			fmt.Printf("rewritten=%q\n", s.Rewritten)
			fmt.Printf("expanded=%q\n", s.Expanded)
			fmt.Printf("results=%d\n", len(s.Results))
			fmt.Printf("context=%q\n", s.Context)
			return nil
		}},
	)

	s := &chain.State{Query: "如何提高Python代码效率"}
	if err := c.Run(ctx, s); err != nil {
		fmt.Printf("chain run error: %v\n", err)
		return
	}
	fmt.Printf("timings=%v\n", s.Timings)
}
