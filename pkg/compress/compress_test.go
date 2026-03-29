package compress

import (
	"context"
	"testing"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }

func TestRuleCompressor(t *testing.T) {
	c := &RuleCompressor{}
	resp, err := c.Compress(context.Background(), CompressRequest{
		Query:    "Python 代码 效率",
		MaxChars: 200,
		Items: []Item{
			{ID: "1", Title: "doc1", Content: "Python 循环效率可以通过列表推导式提升。内存管理可以用生成器。无关内容。", Tags: []string{"python", "性能优化"}},
			{ID: "2", Title: "doc2", Content: "Go 的性能特点与 Python 不同。", Tags: []string{"go"}},
		},
	})
	if err != nil {
		t.Fatalf("Compress error: %v", err)
	}
	if resp.Compressed == "" {
		t.Fatalf("expected compressed")
	}
	if resp.CompressedChars > 200 {
		t.Fatalf("expected <= 200 chars, got %d", resp.CompressedChars)
	}
}

func TestLLMCompressor(t *testing.T) {
	c := &LLMCompressor{LLM: &fakeLLM{resp: "- 结论: 使用列表推导式优化循环\n- 内存: 使用生成器减少峰值"}}
	resp, err := c.Compress(context.Background(), CompressRequest{
		Query:    "如何提高Python代码效率",
		MaxChars: 80,
		LLMModel: "fake",
		Items: []Item{
			{ID: "1", Title: "doc1", Content: "Python 循环效率可以通过列表推导式提升。内存管理可以用生成器。"},
		},
	})
	if err != nil {
		t.Fatalf("Compress error: %v", err)
	}
	if resp.Compressed == "" {
		t.Fatalf("expected compressed")
	}
	if resp.CompressedChars > 80 {
		t.Fatalf("expected <= 80 chars, got %d", resp.CompressedChars)
	}
}
