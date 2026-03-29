package expand

import (
	"context"
	"testing"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error) { return f.resp, nil }

func TestRuleExpander(t *testing.T) {
	e := &RuleExpander{Synonyms: map[string][]string{"减肥": {"瘦身", "控制体重", "减脂"}}}
	resp, err := e.Expand(context.Background(), ExpandRequest{Query: "如何减肥", MaxTerms: 10, Separator: " "})
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	if len(resp.Terms) == 0 {
		t.Fatalf("expected terms")
	}
	if resp.Expanded == resp.Original {
		t.Fatalf("expected expanded query to differ")
	}
}

func TestLLMExpander_ParseBars(t *testing.T) {
	e := &LLMExpander{LLM: &fakeLLM{resp: "跑步→运动|慢跑|马拉松|跑鞋"}}
	resp, err := e.Expand(context.Background(), ExpandRequest{Query: "跑步", LLMModel: "fake", MaxTerms: 10, Separator: " "})
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	if len(resp.Terms) != 4 {
		t.Fatalf("expected 4 terms, got %d", len(resp.Terms))
	}
	if resp.Terms[0] != "运动" {
		t.Fatalf("unexpected first term: %q", resp.Terms[0])
	}
}

func TestPRFExpander_ContentPlusTags(t *testing.T) {
	e := &PRFExpander{}
	resp, err := e.Expand(context.Background(), ExpandRequest{
		Query:    "Python代码效率",
		MaxTerms: 6,
		Separator: " ",
		Feedback: &FeedbackContext{
			TopSnippets: []string{"Python 循环 效率 提升 内存 管理 常用库 性能对比"},
			TopTags:     []string{"python", "性能优化"},
		},
	})
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	if len(resp.Terms) == 0 {
		t.Fatalf("expected terms")
	}
	if resp.Expanded == resp.Original {
		t.Fatalf("expected expanded query to differ")
	}
}
