package utils

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Signals
type SignalHandler func(sender any, params ...any)

type SigHandler struct {
	ID      uint
	Handler SignalHandler
}

const (
	evTypeAdd = iota
	evEypeDel
)

type SigHandlerEvent struct {
	EvType     int
	SignalName string
	SigHandler SigHandler
}

type SignalsOptions struct {
	Logger     *zap.Logger
	Async      bool
	Workers    int
	QueueSize  int
	DropOnFull bool
}

type emitEvent struct {
	Name   string
	Sender any
	Params []any
}

type Signals struct {
	mu sync.RWMutex

	lastID      uint
	sigHandlers map[string][]SigHandler

	logger *zap.Logger

	async      bool
	workers    int
	queueSize  int
	dropOnFull bool
	queue      chan emitEvent
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	// Legacy fields kept for compatibility with previous deferred connect/disconnect behavior.
	// They are no longer required for correctness but remain to avoid disruptive changes.
	inLoop bool
	events []SigHandlerEvent
}

var sig *Signals

func init() {
	Sig()
}

func Sig() *Signals {
	if sig == nil {
		sig = NewSignals()
	}
	return sig
}

func NewSignals() *Signals {
	return NewSignalsWithOptions(SignalsOptions{})
}

func NewSignalsWithOptions(opt SignalsOptions) *Signals {
	lg := opt.Logger
	if lg == nil {
		lg = zap.NewNop()
	}
	workers := opt.Workers
	if workers <= 0 {
		workers = 4
	}
	queueSize := opt.QueueSize
	if queueSize <= 0 {
		queueSize = 256
	}

	s := &Signals{
		lastID:      0,
		sigHandlers: map[string][]SigHandler{},
		inLoop:      false,
		events:      []SigHandlerEvent{},
		logger:      lg,
		async:       opt.Async,
		workers:     workers,
		queueSize:   queueSize,
		dropOnFull:  opt.DropOnFull,
	}
	if s.async {
		_ = s.Start()
	}
	return s
}

func (s *Signals) SetLogger(lg *zap.Logger) {
	if lg == nil {
		lg = zap.NewNop()
	}
	s.mu.Lock()
	s.logger = lg
	s.mu.Unlock()
}

func (s *Signals) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.queue != nil {
		return nil
	}
	if !s.async {
		return nil
	}

	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.queue = make(chan emitEvent, s.queueSize)

	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.workerLoop(i)
	}
	return nil
}

func (s *Signals) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel == nil {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	s.cancel = nil
	s.queue = nil
	s.ctx = nil
	s.mu.Unlock()

	cancel()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *Signals) processEvents() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.events) <= 0 || s.inLoop {
		return
	}
	defer func() {
		s.events = nil
	}()

	for _, v := range s.events {
		sigs, ok := s.sigHandlers[v.SignalName]
		if !ok {
			sigs = make([]SigHandler, 0)
		}
		switch v.EvType {
		case evTypeAdd:
			sigs = append(sigs, v.SigHandler)
		case evEypeDel:
			for i := 0; i < len(sigs); i++ {
				if sigs[i].ID == v.SigHandler.ID {
					sigs = append(sigs[0:i], sigs[i+1:]...)
					break
				}
			}
		}
		s.sigHandlers[v.SignalName] = sigs
	}
}

func (s *Signals) Connect(event string, handler SignalHandler) uint {
	if handler == nil {
		return 0
	}

	s.mu.Lock()
	s.lastID += 1
	ev := SigHandlerEvent{
		EvType:     evTypeAdd,
		SignalName: event,
		SigHandler: SigHandler{
			ID:      s.lastID,
			Handler: handler,
		},
	}
	s.events = append(s.events, ev)
	s.mu.Unlock()

	s.processEvents()
	return s.lastID
}

func (s *Signals) Disconnect(event string, id uint) {
	if id == 0 {
		return
	}

	s.mu.Lock()
	ev := SigHandlerEvent{
		EvType:     evEypeDel,
		SignalName: event,
		SigHandler: SigHandler{
			ID: id,
		},
	}
	s.events = append(s.events, ev)
	s.mu.Unlock()

	s.processEvents()
}

func (s *Signals) Clear(events ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, event := range events {
		delete(s.sigHandlers, event)
	}
}

func (s *Signals) Emit(event string, sender any, params ...any) {
	if event == "" {
		return
	}

	s.mu.RLock()
	async := s.async && s.queue != nil
	queue := s.queue
	dropOnFull := s.dropOnFull
	lg := s.logger
	s.mu.RUnlock()

	if async {
		ev := emitEvent{Name: event, Sender: sender, Params: params}
		if dropOnFull {
			select {
			case queue <- ev:
			default:
				lg.Warn("signals queue full, dropping event", zap.String("event", event))
			}
			return
		}
		select {
		case queue <- ev:
			return
		default:
			// Backpressure: block if buffer is full and DropOnFull=false.
		}
		queue <- ev
		return
	}
	s.dispatch(event, sender, params...)
}

func (s *Signals) dispatch(event string, sender any, params ...any) {
	// Keep legacy semantics: during handler invocation, mark inLoop=true and
	// defer applying Connect/Disconnect events until after the dispatch.
	s.mu.Lock()
	s.inLoop = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inLoop = false
		s.mu.Unlock()
		s.processEvents()
	}()

	s.mu.RLock()
	sigs := append([]SigHandler(nil), s.sigHandlers[event]...)
	lg := s.logger
	s.mu.RUnlock()

	if len(sigs) == 0 {
		return
	}

	for _, h := range sigs {
		if h.Handler == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					lg.Error("signal handler panic", zap.String("event", event), zap.Any("panic", r), zap.ByteString("stack", debug.Stack()))
				}
			}()
			h.Handler(sender, params...)
		}()
	}
}

func (s *Signals) workerLoop(workerID int) {
	defer s.wg.Done()

	for {
		s.mu.RLock()
		ctx := s.ctx
		queue := s.queue
		lg := s.logger
		s.mu.RUnlock()

		if ctx == nil || queue == nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case ev := <-queue:
			start := time.Now()
			s.dispatch(ev.Name, ev.Sender, ev.Params...)
			if d := time.Since(start); d > 2*time.Second {
				lg.Warn("signal handler dispatch slow", zap.String("event", ev.Name), zap.Int("worker", workerID), zap.Duration("latency", d))
			}
		}
	}
}

func (s *Signals) EnableAsync(workers, queueSize int, dropOnFull bool) error {
	s.mu.Lock()
	if workers > 0 {
		s.workers = workers
	}
	if queueSize > 0 {
		s.queueSize = queueSize
	}
	s.dropOnFull = dropOnFull
	s.async = true
	s.mu.Unlock()

	return s.Start()
}

func (s *Signals) DisableAsync(ctx context.Context) error {
	s.mu.Lock()
	s.async = false
	s.mu.Unlock()

	if ctx == nil {
		return errors.New("ctx is nil")
	}
	return s.Shutdown(ctx)
}

func (s *Signals) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("Signals(async=%v, workers=%d, queueSize=%d)", s.async, s.workers, s.queueSize)
}
