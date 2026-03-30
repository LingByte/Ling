package selfquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/LingByte/Ling/pkg/llm"
)

type Extractor struct {
	LLM llm.LLMHandler

	AllowedFields []string
}

type llmOutput struct {
	Query   string     `json:"query"`
	Filters FilterSpec `json:"filters"`
}

func NewExtractor(h llm.LLMHandler, allowedFields []string) *Extractor {
	return &Extractor{LLM: h, AllowedFields: allowedFields}
}

func (e *Extractor) Extract(ctx context.Context, question string, opt *Options) (*Result, error) {
	if e == nil || e.LLM == nil {
		return nil, errors.New("llm handler is nil")
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("question is empty")
	}

	model := ""
	maxJSON := 16_000
	allowed := e.AllowedFields
	if opt != nil {
		model = strings.TrimSpace(opt.Model)
		if opt.MaxJSONChars > 0 {
			maxJSON = opt.MaxJSONChars
		}
		if len(opt.AllowedFields) > 0 {
			allowed = opt.AllowedFields
		}
	}

	prompt := buildPrompt(question, allowed)
	out, err := e.LLM.Query(prompt, model)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	jsonText := extractJSON(out, maxJSON)
	if jsonText == "" {
		jsonText = out
	}

	var parsed llmOutput
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		return &Result{Raw: out}, fmt.Errorf("parse selfquery json failed: %w", err)
	}
	parsed.Query = strings.TrimSpace(parsed.Query)
	if parsed.Query == "" {
		parsed.Query = question
	}

	filters := specToQdrantFilter(parsed.Filters)

	return &Result{
		Query:   parsed.Query,
		Filters: filters,
		Spec:    parsed.Filters,
		Raw:     out,
	}, nil
}

func buildPrompt(question string, allowedFields []string) string {
	fields := make([]string, 0, len(allowedFields))
	for _, f := range allowedFields {
		f = strings.TrimSpace(f)
		if f != "" {
			fields = append(fields, f)
		}
	}
	sort.Strings(fields)

	fieldsLine := ""
	if len(fields) > 0 {
		fieldsLine = "允许字段（只允许从这里选）：" + strings.Join(fields, ", ") + "\n"
	}

	// Important: force JSON only.
	return "你是一个检索自查询(Self-Query)抽取器。\n" +
		"目标：把用户问题抽取为：核心检索 query + 结构化过滤条件 filters。\n" +
		"输出必须是严格 JSON，不要解释，不要 markdown。\n" +
		"JSON schema：{" +
		"\"query\": string, " +
		"\"filters\": {" +
		"\"namespace\"?: string, " +
		"\"source\"?: string, " +
		"\"doc_type\"?: string, " +
		"\"location\"?: string, " +
		"\"years\"?: string[], " +
		"\"dates\"?: string[], " +
		"\"tags_any\"?: string[]" +
		"}" +
		"}\n" +
		fieldsLine +
		"用户问题：" + question + "\n"
}

func specToQdrantFilter(s FilterSpec) map[string]any {
	must := make([]any, 0)
	should := make([]any, 0)

	addShould := func(key string, match any) {
		should = append(should, map[string]any{"key": key, "match": match})
	}
	addMust := func(key string, match any) {
		must = append(must, map[string]any{"key": key, "match": match})
	}

	if strings.TrimSpace(s.Source) != "" {
		addMust("source", map[string]any{"value": strings.TrimSpace(s.Source)})
	}
	if strings.TrimSpace(s.DocType) != "" {
		addMust("metadata.doc_type", map[string]any{"value": strings.TrimSpace(s.DocType)})
		addShould("tags", map[string]any{"any": []string{"type:" + strings.TrimSpace(s.DocType)}})
	}
	if strings.TrimSpace(s.Location) != "" {
		addMust("metadata.location", map[string]any{"value": strings.TrimSpace(s.Location)})
		addShould("tags", map[string]any{"any": []string{"loc:" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s.Location), " ", "_"))}})
	}
	if len(s.Years) > 0 {
		years := dedupStrings(s.Years)
		addShould("metadata.years", map[string]any{"any": years})
		tags := make([]string, 0, len(years))
		for _, y := range years {
			tags = append(tags, "year:"+y)
		}
		addShould("tags", map[string]any{"any": tags})
	}
	if len(s.Dates) > 0 {
		dates := dedupStrings(s.Dates)
		addShould("metadata.dates", map[string]any{"any": dates})
		tags := make([]string, 0, len(dates))
		for _, d := range dates {
			tags = append(tags, "date:"+d)
		}
		addShould("tags", map[string]any{"any": tags})
	}
	if len(s.TagsAny) > 0 {
		addShould("tags", map[string]any{"any": dedupStrings(s.TagsAny)})
	}

	filter := map[string]any{}
	if len(must) > 0 {
		filter["must"] = must
	}
	if len(should) > 0 {
		// Match the pattern used in retrieval/deriveQueryConstraints: push a must item wrapping should.
		// This keeps behavior consistent with Qdrant expectations.
		if len(must) == 0 {
			filter["must"] = []any{}
		}
		filterMust := filter["must"].([]any)
		filterMust = append(filterMust, map[string]any{"should": should})
		filter["must"] = filterMust
	}

	if len(filter) == 0 {
		return nil
	}
	return filter
}

func dedupStrings(in []string) []string {
	set := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if set[s] {
			continue
		}
		set[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
