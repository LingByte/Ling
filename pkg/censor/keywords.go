package censor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type KeywordRule struct {
	Name     string
	Keyword  string
	Category Category
	Severity Severity
	Action   Action
	Pattern  *regexp.Regexp
}

// LoadKeywordDict loads keyword rules from a txt dictionary file.
//
// Supported line formats (fields separated by tab or '|'):
//   keyword
//   keyword<TAB>category<TAB>severity<TAB>action
//   keyword|category|severity|action
//
// Empty lines and lines starting with '#' are ignored.
// Defaults:
//   category=other, severity=high, action=block
func LoadKeywordDict(path string, caseSensitive bool) ([]KeywordRule, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if err := ensureKeywordDictFile(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	out := make([]KeywordRule, 0)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := splitFields(line)
		if len(fields) == 0 {
			continue
		}
		kw := strings.TrimSpace(fields[0])
		if kw == "" {
			continue
		}
		cat := CategoryOther
		sev := SeverityHigh
		act := ActionBlock
		if len(fields) > 1 && strings.TrimSpace(fields[1]) != "" {
			cat = Category(strings.ToLower(strings.TrimSpace(fields[1])))
		}
		if len(fields) > 2 && strings.TrimSpace(fields[2]) != "" {
			sev = Severity(strings.ToLower(strings.TrimSpace(fields[2])))
		}
		if len(fields) > 3 && strings.TrimSpace(fields[3]) != "" {
			act = Action(strings.ToLower(strings.TrimSpace(fields[3])))
		}
		if act != ActionAllow && act != ActionRedact && act != ActionBlock {
			return nil, fmt.Errorf("invalid action %q at %s:%d", act, path, lineNo)
		}

		pattern := regexp.QuoteMeta(kw)
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile keyword regex failed at %s:%d: %w", path, lineNo, err)
		}
		out = append(out, KeywordRule{
			Name:     "keyword:" + kw,
			Keyword:  kw,
			Category: cat,
			Severity: sev,
			Action:   act,
			Pattern:  re,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func ensureKeywordDictFile(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	// Create parent dirs and write a minimal template.
	dir := filepath.Dir(path)
	if strings.TrimSpace(dir) != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(defaultKeywordDictTemplate), 0o600)
}

const defaultKeywordDictTemplate = "# keyword | category | severity | action\n" +
	"# Supported separators: TAB or |\n" +
	"# action: allow|redact|block\n" +
	"# severity: low|medium|high\n" +
	"# category: pii|violence|sexual|hate|fraud|other\n\n" +
	"赌博|fraud|high|block\n" +
	"洗钱|fraud|high|block\n" +
	"裸聊|sexual|high|block\n\n" +
	"邮箱|pii|medium|redact\n" +
	"手机号|pii|medium|redact\n"

func splitFields(line string) []string {
	if strings.Contains(line, "\t") {
		parts := strings.Split(line, "\t")
		return trimFields(parts)
	}
	if strings.Contains(line, "|") {
		parts := strings.Split(line, "|")
		return trimFields(parts)
	}
	return []string{strings.TrimSpace(line)}
}

func trimFields(in []string) []string {
	out := make([]string, 0, len(in))
	for _, f := range in {
		f = strings.TrimSpace(f)
		out = append(out, f)
	}
	// drop trailing empties
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
