package pipeline

import (
	"context"
	"time"
)

type RouterStep[S any] struct {
	StepName string
	Select   func(ctx context.Context, state S) (string, error)
	Routes   map[string]Step[S]
	Default  Step[S]
}

func (s RouterStep[S]) Name() string { return s.StepName }

func (s RouterStep[S]) Run(ctx context.Context, state S) error {
	if s.Select == nil {
		return nil
	}
	key, err := s.Select(ctx, state)
	if err != nil {
		return err
	}
	if next, ok := s.Routes[key]; ok && next != nil {
		return next.Run(ctx, state)
	}
	if s.Default != nil {
		return s.Default.Run(ctx, state)
	}
	return nil
}

type RetryStep[S any] struct {
	StepName    string
	Inner       Step[S]
	MaxAttempts int
	ShouldRetry func(err error) bool
	Backoff     func(attempt int) time.Duration
}

func (s RetryStep[S]) Name() string { return s.StepName }

func (s RetryStep[S]) Run(ctx context.Context, state S) error {
	if s.Inner == nil {
		return nil
	}
	attempts := s.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 1; i <= attempts; i++ {
		if err := s.Inner.Run(ctx, state); err != nil {
			lastErr = err
			if i >= attempts {
				break
			}
			if s.ShouldRetry != nil && !s.ShouldRetry(err) {
				break
			}
			if s.Backoff != nil {
				wait := s.Backoff(i)
				if wait > 0 {
					timer := time.NewTimer(wait)
					select {
					case <-ctx.Done():
						timer.Stop()
						return ctx.Err()
					case <-timer.C:
					}
				}
			}
			continue
		}
		return nil
	}
	return lastErr
}

