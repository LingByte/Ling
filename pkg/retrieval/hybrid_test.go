package retrieval

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/stretchr/testify/assert"
)

type stubVector struct {
	recs  map[string]knowledge.Record
	sleep time.Duration
}

func TestHybridHandler_QueryHybrid_YearBoost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	vec := &stubVector{recs: map[string]knowledge.Record{}}
	h, err := NewHybrid(&HybridOptions{Vector: vec, IndexBasePath: t.TempDir()})
	assert.NoError(t, err)
	defer h.Close()

	recs := []knowledge.Record{
		{ID: "r1", Title: "Policy", Content: "some policy", Source: "s", Tags: []string{"year:2025", "type:policy", "loc:beijing"}, Metadata: map[string]any{"years": []string{"2025"}, "doc_type": "policy", "location": "北京"}},
		{ID: "r2", Title: "Policy", Content: "some policy", Source: "s", Tags: []string{"year:2024", "type:notice"}, Metadata: map[string]any{"years": []string{"2024"}, "doc_type": "notice"}},
	}
	assert.NoError(t, h.Upsert(ctx, recs, &knowledge.UpsertOptions{Namespace: "ns1"}))

	out, err := h.QueryHybrid(ctx, "2025 北京 政策", &knowledge.QueryOptions{Namespace: "ns1", TopK: 2})
	assert.NoError(t, err)
	assert.Len(t, out, 2)
	assert.Equal(t, "r1", out[0].Record.ID)
}

func (s *stubVector) Provider() string { return "stub" }

func (s *stubVector) Upsert(ctx context.Context, records []knowledge.Record, options *knowledge.UpsertOptions) error {
	_ = ctx
	_ = options
	if s.recs == nil {
		s.recs = map[string]knowledge.Record{}
	}
	for _, r := range records {
		s.recs[r.ID] = r
	}
	return nil
}

type stubReranker struct{}

func (s *stubReranker) Rerank(ctx context.Context, query string, documents []string, topN int) ([]knowledge.RerankResult, error) {
	_ = ctx
	_ = query
	if topN <= 0 || topN > len(documents) {
		topN = len(documents)
	}
	out := make([]knowledge.RerankResult, 0, topN)
	for i := 0; i < topN; i++ {
		out = append(out, knowledge.RerankResult{Index: topN - 1 - i, Score: float64(topN - i)})
	}
	return out, nil
}

func (s *stubVector) Query(ctx context.Context, text string, options *knowledge.QueryOptions) ([]knowledge.QueryResult, error) {
	_ = ctx
	_ = text
	_ = options
	if s.sleep > 0 {
		select {
		case <-time.After(s.sleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.recs == nil {
		return nil, nil
	}
	ids := make([]string, 0, len(s.recs))
	for id := range s.recs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]knowledge.QueryResult, 0, len(ids))
	for i, id := range ids {
		r := s.recs[id]
		// Give a stable descending score so the pre-rerank ordering is deterministic: r1 > r2 > r3 ...
		score := 1.0 - float64(i)*0.1
		out = append(out, knowledge.QueryResult{Record: r, Score: score})
	}
	return out, nil
}

func (s *stubVector) Get(ctx context.Context, ids []string, options *knowledge.GetOptions) ([]knowledge.Record, error) {
	_ = ctx
	_ = options
	out := make([]knowledge.Record, 0, len(ids))
	for _, id := range ids {
		if r, ok := s.recs[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *stubVector) List(ctx context.Context, options *knowledge.ListOptions) (*knowledge.ListResult, error) {
	_ = ctx
	_ = options
	return &knowledge.ListResult{}, nil
}

func (s *stubVector) Delete(ctx context.Context, ids []string, options *knowledge.DeleteOptions) error {
	_ = ctx
	_ = options
	for _, id := range ids {
		delete(s.recs, id)
	}
	return nil
}

func TestHybridHandler_UpsertAndQueryHybrid(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	vec := &stubVector{recs: map[string]knowledge.Record{}}
	h, err := NewHybrid(&HybridOptions{Vector: vec, IndexBasePath: t.TempDir()})
	assert.NoError(t, err)
	defer h.Close()

	recs := []knowledge.Record{{ID: "r1", Title: "Hello", Content: "keyword test", Source: "s"}}
	assert.NoError(t, h.Upsert(ctx, recs, &knowledge.UpsertOptions{Namespace: "ns1"}))

	out, err := h.QueryHybrid(ctx, "keyword", &knowledge.QueryOptions{Namespace: "ns1", TopK: 5})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(out), 1)
	assert.Equal(t, "r1", out[0].Record.ID)
}

func TestHybridHandler_QueryHybrid_VectorTimeoutKeywordStillWorks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	vec := &stubVector{recs: map[string]knowledge.Record{}, sleep: 200 * time.Millisecond}
	h, err := NewHybrid(&HybridOptions{Vector: vec, IndexBasePath: t.TempDir(), VectorTimeout: 10 * time.Millisecond, KeywordTimeout: 1 * time.Second, QueryTimeout: 2 * time.Second})
	assert.NoError(t, err)
	defer h.Close()

	recs := []knowledge.Record{{ID: "r1", Title: "Hello", Content: "keyword test", Source: "s"}}
	assert.NoError(t, h.Upsert(ctx, recs, &knowledge.UpsertOptions{Namespace: "ns1"}))

	out, err := h.QueryHybrid(ctx, "keyword", &knowledge.QueryOptions{Namespace: "ns1", TopK: 5})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(out), 1)
}

func TestHybridHandler_QueryHybrid_RerankReorders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	vec := &stubVector{recs: map[string]knowledge.Record{}}
	h, err := NewHybrid(&HybridOptions{Vector: vec, Reranker: &stubReranker{}, IndexBasePath: t.TempDir(), RerankTopN: 10})
	assert.NoError(t, err)
	defer h.Close()

	recs := []knowledge.Record{
		{ID: "r1", Title: "Doc1", Content: "alpha", Source: "s"},
		{ID: "r2", Title: "Doc2", Content: "beta", Source: "s"},
		{ID: "r3", Title: "Doc3", Content: "gamma", Source: "s"},
	}
	assert.NoError(t, h.Upsert(ctx, recs, &knowledge.UpsertOptions{Namespace: "ns1"}))

	out, err := h.QueryHybrid(ctx, "alpha", &knowledge.QueryOptions{Namespace: "ns1", TopK: 3})
	assert.NoError(t, err)
	assert.Len(t, out, 3)
	assert.Equal(t, "r3", out[0].Record.ID)
	assert.Equal(t, true, out[0].Record.Metadata["reranked"])

	ids := []string{out[0].Record.ID, out[1].Record.ID, out[2].Record.ID}
	// After rerank, order should not be the same as alphabetical insert order necessarily; we just assert it's a permutation.
	sort.Strings(ids)
	assert.Equal(t, []string{"r1", "r2", "r3"}, ids)
}
