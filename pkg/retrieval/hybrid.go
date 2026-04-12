package retrieval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/search"
)

var ErrInvalidConfig = errors.New("invalid config")

type HybridOptions struct {
	KnowledgeProvider string
	Vector            knowledge.KnowledgeHandler
	Reranker          Reranker
	IndexBasePath     string
	DefaultAnalyzer   string
	DefaultFields     []string
	VectorWeight      float64
	KeywordWeight     float64
	VectorTopK        int
	KeywordTopK       int
	RerankTopN        int
	QueryTimeout      time.Duration
	VectorTimeout     time.Duration
	KeywordTimeout    time.Duration
}

type HybridHandler struct {
	knowledgeProvider string
	vector            knowledge.KnowledgeHandler
	reranker          Reranker
	indexBasePath     string
	defaultAnalyzer   string
	defaultFields     []string
	vectorWeight      float64
	keywordWeight     float64
	vectorTopK        int
	keywordTopK       int
	rerankTopN        int
	queryTimeout      time.Duration
	vectorTimeout     time.Duration
	keywordTimeout    time.Duration

	mu      sync.Mutex
	engines map[string]search.Engine
}

func NewHybrid(opts *HybridOptions) (*HybridHandler, error) {
	if opts == nil {
		return nil, ErrInvalidConfig
	}
	if opts.Vector == nil {
		return nil, fmt.Errorf("vector knowledge handler is required: %w", ErrInvalidConfig)
	}
	if strings.TrimSpace(opts.IndexBasePath) == "" {
		return nil, fmt.Errorf("IndexBasePath is required: %w", ErrInvalidConfig)
	}
	if err := os.MkdirAll(opts.IndexBasePath, 0o755); err != nil {
		return nil, err
	}

	vw := opts.VectorWeight
	kw := opts.KeywordWeight
	if vw <= 0 && kw <= 0 {
		vw = 0.6
		kw = 0.4
	}
	if vw < 0 {
		vw = 0
	}
	if kw < 0 {
		kw = 0
	}
	if vw+kw == 0 {
		vw = 0.6
		kw = 0.4
	}

	vt := opts.VectorTopK
	kt := opts.KeywordTopK
	if vt <= 0 {
		vt = 30
	}
	if kt <= 0 {
		kt = 30
	}

	rt := opts.RerankTopN
	if rt <= 0 {
		rt = 50
	}

	qt := opts.QueryTimeout
	if qt <= 0 {
		qt = 8 * time.Second
	}
	vto := opts.VectorTimeout
	if vto <= 0 {
		vto = 6 * time.Second
	}
	kto := opts.KeywordTimeout
	if kto <= 0 {
		kto = 3 * time.Second
	}

	prov := strings.TrimSpace(opts.KnowledgeProvider)
	if prov == "" {
		prov = opts.Vector.Provider()
	}

	a := strings.TrimSpace(opts.DefaultAnalyzer)
	if a == "" {
		a = "standard"
	}
	fields := opts.DefaultFields
	if len(fields) == 0 {
		fields = []string{"title", "content", "tags", "source", "record_id"}
	}

	return &HybridHandler{
		knowledgeProvider: prov,
		vector:            opts.Vector,
		reranker:          opts.Reranker,
		indexBasePath:     opts.IndexBasePath,
		defaultAnalyzer:   a,
		defaultFields:     fields,
		vectorWeight:      vw,
		keywordWeight:     kw,
		vectorTopK:        vt,
		keywordTopK:       kt,
		rerankTopN:        rt,
		queryTimeout:      qt,
		vectorTimeout:     vto,
		keywordTimeout:    kto,
		engines:           map[string]search.Engine{},
	}, nil
}

type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]knowledge.RerankResult, error)
}

func (h *HybridHandler) KnowledgeProvider() string {
	return h.knowledgeProvider
}

func (h *HybridHandler) Upsert(ctx context.Context, records []knowledge.Record, options *knowledge.UpsertOptions) error {
	if h == nil || h.vector == nil {
		return errors.New("handler is nil")
	}
	if err := h.vector.Upsert(ctx, records, options); err != nil {
		return err
	}
	eng, err := h.engineForNamespace(namespaceFromUpsert(options))
	if err != nil {
		return err
	}
	docs := make([]search.Doc, 0, len(records))
	for _, r := range records {
		docs = append(docs, recordToDoc(r))
	}
	return eng.IndexBatch(ctx, docs)
}

func (h *HybridHandler) Delete(ctx context.Context, ids []string, options *knowledge.DeleteOptions) error {
	if h == nil || h.vector == nil {
		return errors.New("handler is nil")
	}
	if err := h.vector.Delete(ctx, ids, options); err != nil {
		return err
	}
	eng, err := h.engineForNamespace(namespaceFromDelete(options))
	if err != nil {
		return err
	}
	for _, id := range ids {
		_ = eng.Delete(ctx, id)
	}
	return nil
}

func (h *HybridHandler) QueryHybrid(ctx context.Context, query string, options *knowledge.QueryOptions) ([]knowledge.QueryResult, error) {
	if h == nil || h.vector == nil {
		return nil, errors.New("handler is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is empty")
	}

	ns := namespaceFromQuery(options)
	eng, err := h.engineForNamespace(ns)
	if err != nil {
		return nil, err
	}

	// Overall timeout.
	qctx := ctx
	if h.queryTimeout > 0 {
		var cancel context.CancelFunc
		qctx, cancel = context.WithTimeout(ctx, h.queryTimeout)
		defer cancel()
	}

	vectorTopK := h.vectorTopK
	keywordTopK := h.keywordTopK
	if options != nil && options.TopK > 0 {
		// When caller requests TopK, use it as final K, but allow recall to be bigger.
		vectorTopK = maxInt(vectorTopK, options.TopK)
		keywordTopK = maxInt(keywordTopK, options.TopK)
	}

	// Derive structured constraints from query (rule-based): year tags + metadata filters.
	derived := deriveQueryConstraints(query)
	vectorFilters := MergeQdrantFilters(nil, options.Filters)
	vectorFilters = MergeQdrantFilters(vectorFilters, derived.QdrantFilter)

	kwReq := search.SearchRequest{Keyword: query, SearchFields: h.defaultFields, Size: keywordTopK, Highlight: true}
	if len(derived.TagShould) > 0 {
		kwReq.ShouldTerms = map[string][]string{"tags": derived.TagShould}
		if derived.MinShould > 0 {
			kwReq.MinShould = derived.MinShould
		}
		// Boost tags additionally through query string.
		boost := 3.0
		qs := make([]string, 0, len(derived.TagShould))
		for _, t := range derived.TagShould {
			qs = append(qs, "tags:(\""+t+"\")")
		}
		kwReq.QueryString = &search.ClauseQueryString{Query: strings.Join(qs, " OR "), Boost: &boost}
	}

	var (
		vRes []knowledge.QueryResult
		kRes search.SearchResult
		vErr error
		kErr error
	)
	type vecOut struct {
		res []knowledge.QueryResult
		err error
	}
	type kwOut struct {
		res search.SearchResult
		err error
	}
	vecCh := make(chan vecOut, 1)
	kwCh := make(chan kwOut, 1)

	go func() {
		vctx := qctx
		if h.vectorTimeout > 0 {
			var cancel context.CancelFunc
			vctx, cancel = context.WithTimeout(qctx, h.vectorTimeout)
			defer cancel()
		}
		res, err := h.vector.Query(vctx, query, &knowledge.QueryOptions{Namespace: ns, TopK: vectorTopK, Filters: vectorFilters})
		vecCh <- vecOut{res: res, err: err}
	}()

	go func() {
		kctx := qctx
		if h.keywordTimeout > 0 {
			var cancel context.CancelFunc
			kctx, cancel = context.WithTimeout(qctx, h.keywordTimeout)
			defer cancel()
		}
		res, err := eng.Search(kctx, kwReq)
		kwCh <- kwOut{res: res, err: err}
	}()

	for i := 0; i < 2; i++ {
		select {
		case v := <-vecCh:
			vRes, vErr = v.res, v.err
		case k := <-kwCh:
			kRes, kErr = k.res, k.err
		case <-qctx.Done():
			// Overall timeout/cancel. Keep whatever has returned so far.
			if vErr == nil && vRes == nil {
				vErr = qctx.Err()
			}
			if kErr == nil && kRes.Hits == nil {
				kErr = qctx.Err()
			}
			i = 2
		}
	}
	if vErr != nil && kErr != nil {
		return nil, fmt.Errorf("vector query failed: %v; keyword query failed: %v", vErr, kErr)
	}

	merged := map[string]*mergedResult{}
	for _, r := range vRes {
		id := strings.TrimSpace(r.Record.ID)
		if id == "" {
			continue
		}
		m := merged[id]
		if m == nil {
			m = &mergedResult{record: r.Record}
			merged[id] = m
		}
		m.vectorScore = r.Score
	}

	kwMax := 0.0
	kwIDs := make([]string, 0, len(kRes.Hits))
	for _, hit := range kRes.Hits {
		id := strings.TrimSpace(hit.ID)
		if id == "" {
			continue
		}
		if hit.Score > kwMax {
			kwMax = hit.Score
		}
		m := merged[id]
		if m == nil {
			m = &mergedResult{}
			merged[id] = m
			kwIDs = append(kwIDs, id)
		}
		m.keywordScore = hit.Score
		m.keywordFragments = hit.Fragments
	}

	// Fill missing records from vector store for keyword-only hits.
	if len(kwIDs) > 0 {
		recs, _ := h.vector.Get(ctx, kwIDs, &knowledge.GetOptions{Namespace: ns})
		for _, r := range recs {
			if r.ID == "" {
				continue
			}
			m := merged[r.ID]
			if m != nil && m.record.ID == "" {
				m.record = r
			}
		}
	}

	items := make([]*mergedResult, 0, len(merged))
	for _, v := range merged {
		items = append(items, v)
	}

	maxVec := 0.0
	for _, it := range items {
		if it.vectorScore > maxVec {
			maxVec = it.vectorScore
		}
	}
	for _, it := range items {
		it.finalScore = h.vectorWeight*normalize(it.vectorScore, maxVec) + h.keywordWeight*normalize(it.keywordScore, kwMax)
		if it.record.Metadata == nil {
			it.record.Metadata = map[string]any{}
		}
		it.record.Metadata["vector_score"] = it.vectorScore
		it.record.Metadata["keyword_score"] = it.keywordScore
		it.record.Metadata["final_score"] = it.finalScore
		if len(it.keywordFragments) > 0 {
			it.record.Metadata["keyword_fragments"] = it.keywordFragments
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].finalScore > items[j].finalScore })

	// Optional rerank stage.
	if h.reranker != nil && len(items) > 1 {
		topN := h.rerankTopN
		if topN > len(items) {
			topN = len(items)
		}
		docs := make([]string, 0, topN)
		for i := 0; i < topN; i++ {
			docs = append(docs, strings.TrimSpace(items[i].record.Title+"\n"+items[i].record.Content))
		}

		rctx := qctx
		if h.queryTimeout > 0 {
			// Rerank should respect the overall deadline.
			rctx = qctx
		}
		rr, err := h.reranker.Rerank(rctx, query, docs, topN)
		if err == nil && len(rr) > 0 {
			order := make([]int, 0, len(rr))
			for _, r := range rr {
				if r.Index >= 0 && r.Index < topN {
					order = append(order, r.Index)
				}
			}
			if len(order) > 0 {
				seen := map[int]bool{}
				newItems := make([]*mergedResult, 0, len(items))
				for _, idx := range order {
					if seen[idx] {
						continue
					}
					seen[idx] = true
					items[idx].finalScore = float64(len(order)) - float64(len(newItems))
					items[idx].record.Metadata["final_score"] = items[idx].finalScore
					items[idx].record.Metadata["reranked"] = true
					newItems = append(newItems, items[idx])
				}
				// Append remaining in previous order.
				for i := 0; i < len(items); i++ {
					if seen[i] {
						continue
					}
					newItems = append(newItems, items[i])
				}
				items = newItems
			}
		}
	}

	finalK := 10
	if options != nil && options.TopK > 0 {
		finalK = options.TopK
	}
	if finalK > len(items) {
		finalK = len(items)
	}
	out := make([]knowledge.QueryResult, 0, finalK)
	for i := 0; i < finalK; i++ {
		out = append(out, knowledge.QueryResult{Record: items[i].record, Score: items[i].finalScore})
	}
	return out, nil
}

func (h *HybridHandler) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ns, e := range h.engines {
		_ = e.Close()
		delete(h.engines, ns)
	}
	return nil
}

type mergedResult struct {
	record           knowledge.Record
	vectorScore      float64
	keywordScore     float64
	finalScore       float64
	keywordFragments map[string][]string
}

func recordToDoc(r knowledge.Record) search.Doc {
	fields := map[string]interface{}{
		"record_id": r.ID,
		"title":     r.Title,
		"content":   r.Content,
		"source":    r.Source,
	}
	if len(r.Tags) > 0 {
		// Keep tags as string slice so Bleve indexes each token for term queries.
		fields["tags"] = r.Tags
	}
	return search.Doc{ID: r.ID, Type: "knowledge", Fields: fields}
}

type queryConstraints struct {
	TagShould    []string
	QdrantFilter map[string]any
	MinShould    int
}

var reQueryYear = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
var reQueryISODate = regexp.MustCompile(`\b(19\d{2}|20\d{2})[-/.](0?[1-9]|1[0-2])[-/.](0?[1-9]|[12]\d|3[01])\b`)
var reQueryCNDate = regexp.MustCompile(`\b(19\d{2}|20\d{2})年(0?[1-9]|1[0-2])月(0?[1-9]|[12]\d|3[01])日\b`)
var reQueryLocSuffix = regexp.MustCompile(`\b([\p{Han}]{2,8}(?:市|省|自治区|县|区|镇|乡))\b`)

func deriveQueryConstraints(q string) queryConstraints {
	q = strings.TrimSpace(q)
	if q == "" {
		return queryConstraints{}
	}

	// Derive candidate tags.
	tags := make([]string, 0)
	filterMust := make([]any, 0)
	minShould := 0

	years := reQueryYear.FindAllString(q, -1)
	if len(years) > 0 {
		set := map[string]bool{}
		for _, y := range years {
			set[y] = true
		}
		uniqYears := make([]string, 0, len(set))
		for y := range set {
			uniqYears = append(uniqYears, y)
		}
		sort.Strings(uniqYears)
		yearTags := make([]string, 0, len(uniqYears))
		for _, y := range uniqYears {
			yearTags = append(yearTags, "year:"+y)
		}
		tags = append(tags, yearTags...)
		minShould++
		filterMust = append(filterMust, map[string]any{
			"should": []any{
				map[string]any{"key": "metadata.years", "match": map[string]any{"any": uniqYears}},
				map[string]any{"key": "tags", "match": map[string]any{"any": yearTags}},
			},
		})
	}

	// Dates
	dateSet := map[string]bool{}
	for _, d := range reQueryISODate.FindAllString(q, -1) {
		dateSet[d] = true
	}
	for _, d := range reQueryCNDate.FindAllString(q, -1) {
		dateSet[d] = true
	}
	if len(dateSet) > 0 {
		dates := make([]string, 0, len(dateSet))
		for d := range dateSet {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		dtTags := make([]string, 0, len(dates))
		for _, d := range dates {
			dtTags = append(dtTags, "date:"+d)
		}
		tags = append(tags, dtTags...)
		minShould++
		filterMust = append(filterMust, map[string]any{
			"should": []any{
				map[string]any{"key": "metadata.dates", "match": map[string]any{"any": dates}},
				map[string]any{"key": "tags", "match": map[string]any{"any": dtTags}},
			},
		})
	}

	// Location: best-effort
	loc := ""
	if m := reQueryLocSuffix.FindStringSubmatch(q); len(m) >= 2 {
		loc = strings.TrimSpace(m[1])
	}
	if loc != "" {
		locTag := "loc:" + strings.ToLower(strings.ReplaceAll(loc, " ", "_"))
		tags = append(tags, locTag)
		minShould++
		filterMust = append(filterMust, map[string]any{
			"should": []any{
				map[string]any{"key": "metadata.location", "match": map[string]any{"value": loc}},
				map[string]any{"key": "tags", "match": map[string]any{"any": []string{locTag}}},
			},
		})
	}

	// Doc type
	docType := deriveDocTypeFromQuery(q)
	if docType != "" {
		typeTag := "type:" + docType
		tags = append(tags, typeTag)
		minShould++
		filterMust = append(filterMust, map[string]any{
			"should": []any{
				map[string]any{"key": "metadata.doc_type", "match": map[string]any{"value": docType}},
				map[string]any{"key": "tags", "match": map[string]any{"any": []string{typeTag}}},
			},
		})
	}

	tags = dedupStrings(tags)
	if len(tags) == 0 {
		return queryConstraints{}
	}
	filter := map[string]any{}
	if len(filterMust) > 0 {
		filter["must"] = filterMust
	}
	return queryConstraints{TagShould: tags, QdrantFilter: filter, MinShould: minShould}
}

func deriveDocTypeFromQuery(q string) string {
	q = strings.ToLower(q)
	switch {
	case strings.Contains(q, "政策") || strings.Contains(q, "条例") || strings.Contains(q, "办法") || strings.Contains(q, "规定"):
		return "policy"
	case strings.Contains(q, "通知") || strings.Contains(q, "公告") || strings.Contains(q, "通告"):
		return "notice"
	case strings.Contains(q, "纪要") || strings.Contains(q, "会议"):
		return "minutes"
	case strings.Contains(q, "合同") || strings.Contains(q, "协议"):
		return "contract"
	case strings.Contains(q, "报告") || strings.Contains(q, "总结"):
		return "report"
	default:
		return ""
	}
}

// MergeQdrantFilters merges two Qdrant filter objects (shallow combine via must when both set).
func MergeQdrantFilters(a map[string]any, b map[string]any) map[string]any {
	if a == nil {
		a = map[string]any{}
	}
	if b == nil || len(b) == 0 {
		if len(a) == 0 {
			return nil
		}
		return a
	}
	if len(a) == 0 {
		out := map[string]any{}
		for k, v := range b {
			out[k] = v
		}
		return out
	}

	// Combine into must: [a, b]
	return map[string]any{
		"must": []any{a, b},
	}
}

func (h *HybridHandler) engineForNamespace(ns string) (search.Engine, error) {
	if h == nil {
		return nil, errors.New("handler is nil")
	}
	ns = strings.TrimSpace(ns)
	if ns == "" {
		ns = "default"
	}
	safeNS := sanitizeNamespace(ns)

	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.engines[safeNS]; ok {
		return e, nil
	}

	idxPath := filepath.Join(h.indexBasePath, safeNS)
	cfg := search.Config{
		IndexPath:           idxPath,
		DefaultAnalyzer:     h.defaultAnalyzer,
		DefaultSearchFields: h.defaultFields,
		OpenTimeout:         5 * time.Second,
		QueryTimeout:        5 * time.Second,
		BatchSize:           200,
	}
	e, err := search.NewDefault(cfg)
	if err != nil {
		return nil, err
	}
	h.engines[safeNS] = e
	return e, nil
}

func namespaceFromUpsert(o *knowledge.UpsertOptions) string {
	if o == nil {
		return ""
	}
	return o.Namespace
}

func namespaceFromDelete(o *knowledge.DeleteOptions) string {
	if o == nil {
		return ""
	}
	return o.Namespace
}

func namespaceFromQuery(o *knowledge.QueryOptions) string {
	if o == nil {
		return ""
	}
	return o.Namespace
}

var nsSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeNamespace(ns string) string {
	ns = strings.TrimSpace(ns)
	if ns == "" {
		return "default"
	}
	ns = nsSanitizeRe.ReplaceAllString(ns, "_")
	ns = strings.Trim(ns, "._-")
	if ns == "" {
		return "default"
	}
	return ns
}

func normalize(score float64, max float64) float64 {
	if score <= 0 {
		return 0
	}
	if max <= 0 {
		return score
	}
	return score / max
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
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
