package selfquery

import "strings"

func extractJSON(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if maxChars > 0 && len(s) > maxChars {
		s = s[:maxChars]
	}
	if i := strings.Index(s, "```json"); i >= 0 {
		s2 := s[i+len("```json"):]
		if j := strings.Index(s2, "```"); j >= 0 {
			return strings.TrimSpace(s2[:j])
		}
	}
	l := strings.Index(s, "{")
	r := strings.LastIndex(s, "}")
	if l >= 0 && r > l {
		return strings.TrimSpace(s[l : r+1])
	}
	return ""
}
