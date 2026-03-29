package chain

import (
	"context"
	"time"
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

	for _, st := range c.steps {
		if st == nil {
			continue
		}
		if s.Blocked {
			return ErrStop
		}
		name := st.Name()
		var start time.Time
		if c.opts.TrackTimings {
			start = time.Now()
		}
		err := st.Run(ctx, s)
		if c.opts.TrackTimings {
			s.Timings[name] = time.Since(start)
		}
		if err == nil {
			continue
		}
		if err == ErrStop {
			return nil
		}
		if c.opts.StopOnError {
			return err
		}
		s.Errors = append(s.Errors, err)
	}
	return nil
}
