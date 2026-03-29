package rewrite

import (
	"context"
	"testing"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }

func TestRuleRewriter_Normalize(t *testing.T) {
	rw := &RuleRewriter{}
	resp, err := rw.Rewrite(context.Background(), RewriteRequest{Query: " 你好，世界？  ", MaxChars: 50})
	if err != nil {
		t.Fatalf("Rewrite error: %v", err)
	}
	if resp.Rewritten != "你好,世界?" {
		t.Fatalf("unexpected rewritten: %q", resp.Rewritten)
	}
}

func TestLLMRewriter_JSON(t *testing.T) {
	rw := &LLMRewriter{LLM: &fakeLLM{resp: `{"rewritten":"Python 代码优化 循环效率 内存管理","keywords":["Python","代码优化"]}`}, Model: "fake"}
	resp, err := rw.Rewrite(context.Background(), RewriteRequest{Query: "如何提高Python代码效率", LLMModel: "fake", MaxChars: 100})
	if err != nil {
		t.Fatalf("Rewrite error: %v", err)
	}
	if resp.Rewritten == "" {
		t.Fatalf("expected rewritten")
	}
	if len(resp.Keywords) != 2 {
		t.Fatalf("expected keywords")
	}
}

func TestFactory(t *testing.T) {
	_, err := New(ModeLLM, &FactoryOptions{LLM: &fakeLLM{resp: "x"}, LLMModel: "fake"})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	_, err = New(ModeLLM, nil)
	if err == nil {
		t.Fatalf("expected missing llm error")
	}
}
