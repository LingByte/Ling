package extract

import (
	"context"
	"testing"

	"github.com/LingByte/Ling/pkg/llm"
	"github.com/stretchr/testify/assert"
)

type fakeLLM struct{ resp string }

func (f *fakeLLM) Query(text, model string) (string, error)     { return f.resp, nil }
func (f *fakeLLM) Provider() string                             { return "fake" }
func (f *fakeLLM) QueryWithOptions(text string, options *llm.QueryOptions) (*llm.QueryResponse, error) {
	_ = text
	_ = options
	return &llm.QueryResponse{Choices: []llm.QueryChoice{{Index: 0, Content: f.resp}}}, nil
}
func (f *fakeLLM) QueryStream(text string, options *llm.QueryOptions, callback func(segment string, isComplete bool) error) (*llm.QueryResponse, error) {
	_ = text
	_ = options
	if callback != nil {
		_ = callback(f.resp, false)
		_ = callback("", true)
	}
	return &llm.QueryResponse{Choices: []llm.QueryChoice{{Index: 0, Content: f.resp}}}, nil
}
func (f *fakeLLM) Interrupt()                                  {}
func (f *fakeLLM) ResetMemory()                                 {}
func (f *fakeLLM) SummarizeMemory(model string) (string, error) { return "", nil }
func (f *fakeLLM) SetMaxMemoryMessages(n int)                   {}
func (f *fakeLLM) GetMaxMemoryMessages() int                    { return 0 }

var _ llm.LLMHandler = (*fakeLLM)(nil)

func TestFactory_Rule(t *testing.T) {
	e, err := New("rule", nil)
	assert.NoError(t, err)
	assert.Equal(t, "rule", e.Provider())
}

func TestFactory_LLM_Missing(t *testing.T) {
	_, err := New("llm", nil)
	assert.Error(t, err)
}

func TestRuleExtractor_Years(t *testing.T) {
	e := &RuleExtractor{}
	res, err := e.Extract(context.Background(), ChunkInput{ChunkIndex: 0, Text: "2025年的政策在2025-03-01发布"}, &Options{MaxTags: 10})
	assert.NoError(t, err)
	assert.NotNil(t, res.Metadata["years"])
	assert.Contains(t, res.Tags, "year:2025")
}

func TestLLMExtractor_Parse(t *testing.T) {
	llmE := &LLMExtractor{LLM: &fakeLLM{resp: `{"tags":["year:2025"],"metadata":{"years":["2025"],"keywords":["policy"]}}`}, Model: "m"}
	res, err := llmE.Extract(context.Background(), ChunkInput{ChunkIndex: 1, Text: "x"}, &Options{MaxTags: 10})
	assert.NoError(t, err)
	assert.Contains(t, res.Tags, "year:2025")
	assert.Equal(t, 1, res.Metadata["chunk_index"])
}
