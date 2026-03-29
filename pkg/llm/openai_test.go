package llm

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewOpenaiHandler(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("OPENAI_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("missing OPENAI_API_KEY/OPENAI_BASE_URL/OPENAI_MODEL")
	}
	openaiHandler, err := NewOpenaiHandler(context.Background(), &LLMOptions{
		BaseURL: baseURL,
		ApiKey:  apiKey,
	})
	assert.Nil(t, err)
	assert.NotNil(t, openaiHandler)
	assert.NotNil(t, openaiHandler.ctx)
	resp, err := openaiHandler.QueryWithOptions("我是李泽，你是谁", &QueryOptions{
		Model:       model,
		N:           1,
		FilterEmoji: true,
		Temperature: float32(0.7),
	})
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	log.Println(resp.Choices[0].Content)
	resp, err = openaiHandler.QueryWithOptions("告诉我我是谁", &QueryOptions{
		Model:       model,
		N:           1,
		FilterEmoji: true,
		Temperature: float32(0.7),
	})
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	log.Println(resp.Choices[0].Content)
}

func TestOpenaiHandler_AsyncSummaryCompaction_RetainsMarkers(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("OPENAI_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		t.Skip("missing OPENAI_API_KEY/OPENAI_BASE_URL/OPENAI_MODEL")
	}

	oh, err := NewOpenaiHandler(context.Background(), &LLMOptions{
		BaseURL: baseURL,
		ApiKey:  apiKey,
	})
	assert.NoError(t, err)
	assert.NotNil(t, oh)

	// Reduce cap to make the test faster.
	oh.SetMaxMemoryMessages(10)
	for i := 0; i < 15; i++ {
		m := "SPECIAL_KNOWLEDGE_" + strconv.Itoa(i) + "=K" + strconv.Itoa(i)
		_, err := oh.QueryWithOptions("Remember this fact: "+m, &QueryOptions{Model: model, Temperature: float32(0.2)})
		assert.NoError(t, err)
	}

	// Wait for async summarization to run and apply.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		oh.mutex.Lock()
		summary := oh.summary
		msgCount := len(oh.messages)
		oh.mutex.Unlock()
		if summary != "" && msgCount == 0 {
			// Validate at least the marker prefix shows up.
			assert.True(t, strings.Contains(summary, "SPECIAL_KNOWLEDGE"))
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("timed out waiting for async summary compaction")
}
