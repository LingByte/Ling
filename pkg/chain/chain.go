package chain

import (
	"context"
	"time"

	"github.com/LingByte/Ling/pkg/pipeline"
)

type Options struct {
	// If true, stop immediately on the first step error.
	StopOnError bool

	// If true, record step timings into State.Timings.
	TrackTimings bool
}

type Chain struct {
	steps []Step
	opts  Options
}

func New(steps ...Step) *Chain {
	return &Chain{steps: steps, opts: Options{StopOnError: true, TrackTimings: true}}
}

func (c *Chain) WithOptions(opts Options) *Chain {
	if c == nil {
		return &Chain{opts: opts}
	}
	c.opts = opts
	return c
}

func (c *Chain) Append(steps ...Step) *Chain {
	if c == nil {
		return New(steps...)
	}
	c.steps = append(c.steps, steps...)
	return c
}

func (c *Chain) Steps() []Step {
	if c == nil {
		return nil
	}
	out := make([]Step, len(c.steps))
	copy(out, c.steps)
	return out
}

func (c *Chain) Run(ctx context.Context, s *State) error {
	if s == nil {
		s = &State{}
	}
	if c == nil {
		return nil
	}
	if c.opts.TrackTimings && s.Timings == nil {
		s.Timings = map[string]time.Duration{}
	}
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}

	p := pipeline.NewBuilder[*State]().
		WithOptions(pipeline.RunOptions[*State]{
			StopOnError:  c.opts.StopOnError,
			TrackTimings: c.opts.TrackTimings,
			OnStepTiming: func(step string, d time.Duration, st *State) {
				st.Timings[step] = d
			},
			OnStepError: func(_ string, err error, st *State) bool {
				if err == ErrStop {
					return false
				}
				if c.opts.StopOnError {
					return true
				}
				st.Errors = append(st.Errors, err)
				return false
			},
		})
	for _, st := range c.steps {
		if st == nil {
			continue
		}
		p.Add(st)
	}
	pipe := p.Build()

	err := pipe.Run(ctx, s)
	if err == ErrStop {
		return nil
	}
	return err
}
