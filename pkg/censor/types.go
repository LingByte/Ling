package censor

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	ModeRule = "rule"
	ModeLLM  = "llm"
)

type Action string

type Severity string

type Category string

const (
	ActionAllow  Action = "allow"
	ActionRedact Action = "redact"
	ActionBlock  Action = "block"
)

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

const (
	CategoryPII      Category = "pii"
	CategoryViolence Category = "violence"
	CategorySexual   Category = "sexual"
	CategoryHate     Category = "hate"
	CategoryFraud    Category = "fraud"
	CategoryOther    Category = "other"
)

var (
	ErrEmptyText          = errors.New("empty text")
	ErrUnsupportedMode    = errors.New("unsupported mode")
	ErrMissingLLM         = errors.New("missing llm")
	ErrInvalidPolicy      = errors.New("invalid policy")
	ErrMissingAssessment  = errors.New("missing assessment")
	ErrBlocked            = errors.New("blocked")
)

type Policy struct {
	DefaultAction Action
	BlockAtOrAbove Severity
	RedactAtOrAbove Severity
	RedactionMask string

	// If false, rule matching is case-insensitive (recommended).
	CaseSensitive bool
}

type Match struct {
	Category Category
	Severity Severity
	Rule     string
	Span     string
}

type AssessRequest struct {
	Text   string
	Mode   string
	Policy *Policy
	// Strategy-specific options.
	Options map[string]any
}

type AssessResponse struct {
	Action     Action
	Allowed    bool
	Redacted   bool
	Blocked    bool
	Original   string
	Processed  string
	Matches    []Match
	Strategy   string
	Latency    time.Duration
	Debug      map[string]any
}

type Censor interface {
	Assess(ctx context.Context, req AssessRequest) (*AssessResponse, error)
}

type LLM interface {
	Query(text, model string) (string, error)
}

func DefaultPolicy() *Policy {
	return &Policy{
		DefaultAction:   ActionAllow,
		BlockAtOrAbove:  SeverityHigh,
		RedactAtOrAbove: SeverityMedium,
		RedactionMask:   "[REDACTED]",
		CaseSensitive:   false,
	}
}

func normalizeText(s string) string {
	return strings.TrimSpace(s)
}

func severityGE(a, b Severity) bool {
	return severityRank(a) >= severityRank(b)
}

func severityRank(s Severity) int {
	s = Severity(strings.ToLower(string(s)))
	switch s {
	case SeverityLow:
		return 1
	case SeverityMedium:
		return 2
	case SeverityHigh:
		return 3
	default:
		return 0
	}
}
