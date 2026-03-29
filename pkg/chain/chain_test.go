package chain

import (
	"context"
	"errors"
	"testing"
)

type stepRecorder struct {
	name string
	out  *[]string
	err  error
}

func (s stepRecorder) Name() string { return s.name }

func (s stepRecorder) Run(ctx context.Context, st *State) error {
	_ = ctx
	_ = st
	if s.out != nil {
		*s.out = append(*s.out, s.name)
	}
	return s.err
}

func TestChain_Order(t *testing.T) {
	order := []string{}
	c := New(stepRecorder{name: "a", out: &order}, stepRecorder{name: "b", out: &order}, stepRecorder{name: "c", out: &order})
	err := c.Run(context.Background(), &State{Query: "q"})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestChain_StopOnError(t *testing.T) {
	order := []string{}
	someErr := errors.New("x")
	c := New(stepRecorder{name: "a", out: &order}, stepRecorder{name: "b", out: &order, err: someErr}, stepRecorder{name: "c", out: &order}).WithOptions(Options{StopOnError: true, TrackTimings: false})
	err := c.Run(context.Background(), &State{Query: "q"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if len(order) != 2 {
		t.Fatalf("expected stop at b, got %v", order)
	}
}

func TestChain_ContinueOnError(t *testing.T) {
	order := []string{}
	someErr := errors.New("x")
	c := New(stepRecorder{name: "a", out: &order}, stepRecorder{name: "b", out: &order, err: someErr}, stepRecorder{name: "c", out: &order}).WithOptions(Options{StopOnError: false, TrackTimings: false})
	s := &State{Query: "q"}
	err := c.Run(context.Background(), s)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected all steps, got %v", order)
	}
	if len(s.Errors) != 1 {
		t.Fatalf("expected 1 recorded error")
	}
}
