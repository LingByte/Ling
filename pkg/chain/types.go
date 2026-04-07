package chain

import (
	"errors"
	"time"

	"github.com/LingByte/Ling/pkg/knowledge"
	"github.com/LingByte/Ling/pkg/pipeline"
)

var (
	// ErrStop indicates the chain should stop early without treating it as an error.
	ErrStop = errors.New("stop chain")
)

type State struct {
	Query      string
	Rewritten  string
	Expanded   string
	ExpandTerms []string

	Results   []knowledge.QueryResult
	Context   string
	Answer    string

	Blocked bool

	// Per-step timings.
	Timings map[string]time.Duration
	// Arbitrary bag for custom fields.
	Meta map[string]any
	// Non-fatal errors if chain is configured to continue.
	Errors []error
}

type Step = pipeline.Step[*State]
type StepFunc = pipeline.StepFunc[*State]
type RouterStep = pipeline.RouterStep[*State]
type RetryStep = pipeline.RetryStep[*State]
