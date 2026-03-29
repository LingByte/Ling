package extract

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type RuleExtractor struct{}

func (e *RuleExtractor) Provider() string { return ExtractorRule }

func (e *RuleExtractor) Extract(ctx context.Context, in ChunkInput, opts *Options) (*ExtractResult, error) {
	_ = ctx
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, ErrEmptyText
	}

	res := &ExtractResult{Metadata: map[string]any{}}
	res.Metadata["chunk_index"] = in.ChunkIndex
	if strings.TrimSpace(in.FileName) != "" {
		res.Metadata["file_name"] = in.FileName
	}
	if strings.TrimSpace(in.Source) != "" {
		res.Metadata["source"] = in.Source
	}
	if strings.TrimSpace(in.Namespace) != "" {
		res.Metadata["namespace"] = in.Namespace
	}

	years := extractYears(text)
	if len(years) > 0 {
		res.Metadata["years"] = years
		for _, y := range years {
			res.Tags = append(res.Tags, "year:"+y)
		}
	}

	dates := extractDates(text)
	if len(dates) > 0 {
		res.Metadata["dates"] = dates
		if len(dates) > 0 {
			res.Tags = append(res.Tags, "date:"+dates[0])
		}
	}

	loc := extractLocationByRule(text)
	if loc != "" {
		res.Metadata["location"] = loc
		res.Tags = append(res.Tags, "loc:"+normalizeTagToken(loc))
	}

	dt := extractDocType(text)
	if dt != "" {
		res.Metadata["doc_type"] = dt
		res.Tags = append(res.Tags, "type:"+dt)
	}

	kw := extractKeywords(text, 8)
	if len(kw) > 0 {
		res.Metadata["keywords"] = kw
		for i := 0; i < len(kw) && i < 3; i++ {
			res.Tags = append(res.Tags, "kw:"+normalizeTagToken(kw[i]))
		}
	}

	res.Tags = dedupStrings(res.Tags)
	if opts != nil && opts.MaxTags > 0 && len(res.Tags) > opts.MaxTags {
		res.Tags = res.Tags[:opts.MaxTags]
	}
	return res, nil
}

var reYear = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

func extractYears(s string) []string {
	m := reYear.FindAllString(s, -1)
	if len(m) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, v := range m {
		set[v] = true
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

var (
	reISODate = regexp.MustCompile(`\b(19\d{2}|20\d{2})[-/.](0?[1-9]|1[0-2])[-/.](0?[1-9]|[12]\d|3[01])\b`)
	reCNDate  = regexp.MustCompile(`\b(19\d{2}|20\d{2})年(0?[1-9]|1[0-2])月(0?[1-9]|[12]\d|3[01])日\b`)
)

func extractDates(s string) []string {
	set := map[string]bool{}
	for _, v := range reISODate.FindAllString(s, -1) {
		set[v] = true
	}
	for _, v := range reCNDate.FindAllString(s, -1) {
		set[v] = true
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

var reLocKV = regexp.MustCompile(`(?m)^(地点|地点：|地点:)\s*([^\n]{2,40})$`)
var reLocSuffix = regexp.MustCompile(`\b([\p{Han}]{2,8}(?:市|省|自治区|县|区|镇|乡))\b`)
var reLocIn = regexp.MustCompile(`\b在([\p{Han}]{2,10}(?:市|省|自治区|县|区|镇|乡)?)\b`)

func extractLocationByRule(s string) string {
	m := reLocKV.FindStringSubmatch(s)
	if len(m) >= 3 {
		return strings.TrimSpace(m[2])
	}
	if m2 := reLocSuffix.FindStringSubmatch(s); len(m2) >= 2 {
		return strings.TrimSpace(m2[1])
	}
	if m3 := reLocIn.FindStringSubmatch(s); len(m3) >= 2 {
		v := strings.TrimSpace(m3[1])
		if len([]rune(v)) >= 2 {
			return v
		}
	}
	return ""
}

func extractDocType(s string) string {
	ls := strings.ToLower(s)
	// Chinese heuristics.
	switch {
	case strings.Contains(ls, "政策") || strings.Contains(ls, "条例") || strings.Contains(ls, "办法") || strings.Contains(ls, "规定"):
		return "policy"
	case strings.Contains(ls, "通知") || strings.Contains(ls, "公告") || strings.Contains(ls, "通告"):
		return "notice"
	case strings.Contains(ls, "纪要") || strings.Contains(ls, "会议纪要") || strings.Contains(ls, "会议记录"):
		return "minutes"
	case strings.Contains(ls, "合同") || strings.Contains(ls, "协议"):
		return "contract"
	case strings.Contains(ls, "报告") || strings.Contains(ls, "总结"):
		return "report"
	default:
		return ""
	}
}

func extractKeywords(s string, limit int) []string {
	// Very lightweight keyword extraction: pick top frequent tokens with length>=2.
	tokens := tokenize(s)
	freq := map[string]int{}
	for _, t := range tokens {
		if len([]rune(t)) < 2 {
			continue
		}
		if isStopWord(t) {
			continue
		}
		freq[t]++
	}
	type kv struct {
		k string
		v int
	}
	arr := make([]kv, 0, len(freq))
	for k, v := range freq {
		arr = append(arr, kv{k: k, v: v})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].v == arr[j].v {
			return arr[i].k < arr[j].k
		}
		return arr[i].v > arr[j].v
	})
	if limit <= 0 {
		limit = 8
	}
	if limit > len(arr) {
		limit = len(arr)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, arr[i].k)
	}
	return out
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	b := strings.Builder{}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune(' ')
	}
	parts := strings.Fields(b.String())
	return parts
}

var stopWords = map[string]bool{
	"the": true, "and": true, "or": true, "to": true, "of": true, "in": true, "on": true, "for": true, "is": true,
	"a": true, "an": true, "with": true, "as": true, "at": true, "by": true, "from": true,
	"this": true, "that": true, "these": true, "those": true,
	"we": true, "you": true, "i": true,
}

func isStopWord(s string) bool { return stopWords[s] }

func normalizeTagToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "_")
	return s
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
