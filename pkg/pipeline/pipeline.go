package pipeline

import (
	"context"
	"time"
)

type RunOptions[S any] struct {
	StopOnError   bool
	TrackTimings  bool
	OnStepTiming  func(step string, d time.Duration, state S)
	OnStepError   func(step string, err error, state S) bool
	BeforeEachRun func(step string, state S)
	AfterEachRun  func(step string, state S, err error)
}

type Pipeline[S any] struct {
	steps []Step[S]
	opts  RunOptions[S]
}

func New[S any](steps ...Step[S]) *Pipeline[S] {
	return &Pipeline[S]{
		steps: steps,
		opts: RunOptions[S]{
			StopOnError:  true,
			TrackTimings: true,
		},
	}
}

func (p *Pipeline[S]) WithOptions(opts RunOptions[S]) *Pipeline[S] {
	if p == nil {
		return &Pipeline[S]{opts: opts}
	}
	p.opts = opts
	return p
}

func (p *Pipeline[S]) Append(steps ...Step[S]) *Pipeline[S] {
	if p == nil {
		return New[S](steps...)
	}
	p.steps = append(p.steps, steps...)
	return p
}

func (p *Pipeline[S]) Steps() []Step[S] {
	if p == nil {
		return nil
	}
	out := make([]Step[S], len(p.steps))
	copy(out, p.steps)
	return out
}

func (p *Pipeline[S]) Run(ctx context.Context, state S) error {
	if p == nil {
		return nil
	}
	for _, step := range p.steps {
		if step == nil {
			continue
		}
		name := step.Name()
		if p.opts.BeforeEachRun != nil {
			p.opts.BeforeEachRun(name, state)
		}
		start := time.Now()
		err := step.Run(ctx, state)
		if p.opts.TrackTimings && p.opts.OnStepTiming != nil {
			p.opts.OnStepTiming(name, time.Since(start), state)
		}
		if p.opts.AfterEachRun != nil {
			p.opts.AfterEachRun(name, state, err)
		}
		if err == nil {
			continue
		}
		if p.opts.OnStepError != nil {
			if stop := p.opts.OnStepError(name, err, state); stop {
				return err
			}
			continue
		}
		if p.opts.StopOnError {
			return err
		}
	}
	return nil
}

