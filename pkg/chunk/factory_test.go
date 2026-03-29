package chunk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeChunkLLM struct{}

func (f *fakeChunkLLM) Query(text, model string) (string, error) {
	return `[{"title":"t","text":"x"}]`, nil
}
func (f *fakeChunkLLM) Provider() string { return "fake" }
func (f *fakeChunkLLM) ResetMemory()     {}
func (f *fakeChunkLLM) SummarizeMemory(model string) (string, error) {
	return "", nil
}
func (f *fakeChunkLLM) SetMaxMemoryMessages(n int) {}
func (f *fakeChunkLLM) GetMaxMemoryMessages() int  { return 0 }

func TestFactory_DefaultRule(t *testing.T) {
	c, err := New("", nil)
	assert.NoError(t, err)
	assert.Equal(t, "rule", c.Provider())
}

func TestFactory_Rule(t *testing.T) {
	c, err := New("rule", nil)
	assert.NoError(t, err)
	assert.Equal(t, "rule", c.Provider())
}

func TestFactory_LLM_MissingLLM(t *testing.T) {
	_, err := New("llm", &FactoryOptions{})
	assert.Error(t, err)
}

func TestFactory_LLM_OK(t *testing.T) {
	c, err := New("llm", &FactoryOptions{LLM: &fakeChunkLLM{}, Model: "m"})
	assert.NoError(t, err)
	assert.Equal(t, "llm", c.Provider())
}

func TestFactory_Unsupported(t *testing.T) {
	_, err := New("nope", nil)
	assert.ErrorIs(t, err, ErrUnsupportedChunkerType)
}
