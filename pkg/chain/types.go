package chain

import (
	"context"
	"errors"
	"time"

	"github.com/LingByte/Ling/pkg/knowledge"
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

type Step interface {
	Name() string
	Run(ctx context.Context, s *State) error
}

type StepFunc struct {
	StepName string
	Fn       func(ctx context.Context, s *State) error
}

func (f StepFunc) Name() string { return f.StepName }

func (f StepFunc) Run(ctx context.Context, s *State) error {
	if f.Fn == nil {
		return nil
	}
	return f.Fn(ctx, s)
}
