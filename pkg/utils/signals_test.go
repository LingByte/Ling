package utils

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSignals_Sync(t *testing.T) {
	s := NewSignals()

	var val string
	var eid uint
	eid = s.Connect("mock_test", func(sender any, params ...any) {
		val = sender.(string)
		assert.True(t, s.inLoop)
		s.Disconnect("mock_test", eid)
	})

	s.Emit("mock_test", "unittest")
	assert.Equal(t, "unittest", val)
	assert.Equal(t, 0, len(s.events))

	// Handler should have been removed.
	s.Emit("mock_test", "unittest2")
	assert.Equal(t, "unittest", val)

	s.Clear("mock_test", "test1", "test2")
	assert.Equal(t, 0, len(s.sigHandlers))
}

func TestSignals_Async(t *testing.T) {
	s := NewSignalsWithOptions(SignalsOptions{
		Async:      true,
		Workers:    2,
		QueueSize:  16,
		DropOnFull: false,
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()

	done := make(chan struct{})
	var val atomic.Value
	var eid uint
	eid = s.Connect("mock_test", func(sender any, params ...any) {
		val.Store(sender.(string))
		assert.True(t, s.inLoop)
		s.Disconnect("mock_test", eid)
		close(done)
	})

	s.Emit("mock_test", "unittest")

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for async signal handler")
	}

	v := val.Load()
	if assert.NotNil(t, v) {
		assert.Equal(t, "unittest", v.(string))
	}
	assert.Equal(t, 0, len(s.events))

	// Handler should have been removed.
	s.Emit("mock_test", "unittest2")
	// Give worker a moment in case of unexpected dispatch.
	time.Sleep(50 * time.Millisecond)
	v2 := val.Load()
	if assert.NotNil(t, v2) {
		assert.Equal(t, "unittest", v2.(string))
	}
}
