package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LingByte/Ling/pkg/llm"
)

type LLMExtractor struct {
	LLM   llm.LLMHandler
	Model string
}

func (e *LLMExtractor) Provider() string { return ExtractorLLM }

func (e *LLMExtractor) Extract(ctx context.Context, in ChunkInput, opts *Options) (*ExtractResult, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, ErrEmptyText
	}
	if e == nil || e.LLM == nil {
		return nil, ErrMissingLLM
	}

	model := strings.TrimSpace(e.Model)
	prompt := buildExtractPrompt(in)
	resp, err := e.LLM.Query(prompt, model)
	if err != nil {
		return nil, err
	}
	res, err := parseLLMExtract(resp)
	if err != nil {
		return nil, err
	}
	if res.Metadata == nil {
		res.Metadata = map[string]any{}
	}
	res.Metadata["chunk_index"] = in.ChunkIndex
	res.Tags = dedupStrings(res.Tags)
	if opts != nil && opts.MaxTags > 0 && len(res.Tags) > opts.MaxTags {
		res.Tags = res.Tags[:opts.MaxTags]
	}
	return res, nil
}

func buildExtractPrompt(in ChunkInput) string {
	title := strings.TrimSpace(in.DocumentTitle)
	if title == "" {
		title = strings.TrimSpace(in.FileName)
	}
	return fmt.Sprintf(`You are an information extraction engine.

STRICT OUTPUT RULES:
- Output MUST be a single JSON object and nothing else.
- Do NOT output markdown.
- Do NOT wrap the JSON in code fences.

JSON SCHEMA:
{
  "tags": [string],
  "metadata": {
    "years"?: [string],
    "dates"?: [string],
    "location"?: string,
    "keywords"?: [string],
    "doc_type"?: string
  }
}

EXTRA RULES:
- tags should be short and useful for filtering.
- include year tags like "year:2025" when present.
- include type tags like "type:policy" when applicable.
- include location tags like "loc:beijing" when present.

DOCUMENT TITLE: %s
CHUNK TEXT:
%s
`, title, strings.TrimSpace(in.Text))
}

func parseLLMExtract(s string) (*ExtractResult, error) {
	s = strings.TrimSpace(s)
	// Extract first JSON object.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}

	var raw struct {
		Tags     []string       `json:"tags"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, err
	}
	if raw.Metadata == nil {
		raw.Metadata = map[string]any{}
	}
	out := &ExtractResult{Tags: raw.Tags, Metadata: raw.Metadata}
	if len(out.Tags) == 0 && len(out.Metadata) == 0 {
		return nil, errors.New("empty extract result")
	}
	return out, nil
}
