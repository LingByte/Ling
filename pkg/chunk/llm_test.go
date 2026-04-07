package chunk

import (
	"context"
	"testing"

	"github.com/LingByte/Ling/pkg/llm"
	"github.com/stretchr/testify/assert"
)

type fakeLLM struct {
	resp string
	err  error
}

func (f *fakeLLM) Query(text, model string) (string, error) {
	_ = text
	_ = model
	return f.resp, f.err
}

func (f *fakeLLM) Provider() string { return "fake" }
func (f *fakeLLM) QueryWithOptions(text string, options *llm.QueryOptions) (*llm.QueryResponse, error) {
	_ = text
	_ = options
	return &llm.QueryResponse{Choices: []llm.QueryChoice{{Index: 0, Content: f.resp}}}, f.err
}
func (f *fakeLLM) QueryStream(text string, options *llm.QueryOptions, callback func(segment string, isComplete bool) error) (*llm.QueryResponse, error) {
	_ = text
	_ = options
	if callback != nil {
		_ = callback(f.resp, false)
		_ = callback("", true)
	}
	return &llm.QueryResponse{Choices: []llm.QueryChoice{{Index: 0, Content: f.resp}}}, f.err
}
func (f *fakeLLM) Interrupt()       {}
func (f *fakeLLM) ResetMemory()     {}
func (f *fakeLLM) SummarizeMemory(model string) (string, error) {
	_ = model
	return "", nil
}
func (f *fakeLLM) SetMaxMemoryMessages(n int) { _ = n }
func (f *fakeLLM) GetMaxMemoryMessages() int  { return 0 }

var _ llm.LLMHandler = (*fakeLLM)(nil)

func TestLLMChunker_Chunk_PureJSON(t *testing.T) {
	c := &LLMChunker{LLM: &fakeLLM{resp: `[{"title":"A","text":"hello"},{"title":"B","text":"world"}]`}}
	chunks, err := c.Chunk(context.Background(), "input", &ChunkOptions{MaxChars: 100})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(chunks))
	assert.Equal(t, "A", chunks[0].Title)
	assert.Equal(t, "hello", chunks[0].Text)
}

func TestLLMChunker_Chunk_FencedJSON(t *testing.T) {
	c := &LLMChunker{LLM: &fakeLLM{resp: "```json\n[{\"title\":\"A\",\"text\":\"hello\"}]\n```"}}
	chunks, err := c.Chunk(context.Background(), "input", &ChunkOptions{MaxChars: 100})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(chunks))
	assert.Equal(t, "hello", chunks[0].Text)
}

func TestLLMChunker_Chunk_NoiseAroundJSON(t *testing.T) {
	c := &LLMChunker{LLM: &fakeLLM{resp: "Sure, here you go:\n[{\"title\":\"A\",\"text\":\"hello\"}]\nThanks"}}
	chunks, err := c.Chunk(context.Background(), "input", &ChunkOptions{MaxChars: 100})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(chunks))
	assert.Equal(t, "A", chunks[0].Title)
}
