package llm

import (
	"context"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewOpenaiHandler(t *testing.T) {
	apiKey := ""
	baseURL := ""
	model := ""
	openaiHandler, err := NewOpenaiHandler(context.Background(), &LLMOptions{
		BaseURL: baseURL,
		ApiKey:  apiKey,
	})
	assert.Nil(t, err)
	assert.NotNil(t, openaiHandler)
	assert.NotNil(t, openaiHandler.ctx)
	resp, err := openaiHandler.QueryWithOptions("你好", &QueryOptions{
		Model:        model,
		N:            1,
		FilterEmoji:  true,
		OutputFormat: OutputFormatXML,
	})
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	log.Println(resp.Choices[0].Content)
}
