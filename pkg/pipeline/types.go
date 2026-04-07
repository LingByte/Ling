package pipeline

import "context"

type Step[S any] interface {
	Name() string
	Run(ctx context.Context, state S) error
}

type StepFunc[S any] struct {
	StepName string
	Fn       func(ctx context.Context, state S) error
}

func (f StepFunc[S]) Name() string { return f.StepName }

func (f StepFunc[S]) Run(ctx context.Context, state S) error {
	if f.Fn == nil {
		return nil
	}
	return f.Fn(ctx, state)
}

