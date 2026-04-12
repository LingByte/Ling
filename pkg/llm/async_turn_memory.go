package llm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

const maxSummarizeInputCharsTurn = 12000

func buildTranscriptFromLLMMemory(messages []llmMemoryMessage) string {
	if len(messages) == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range messages {
		role := m.Role
		if role == "" {
			role = "unknown"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
		if b.Len() >= maxSummarizeInputCharsTurn {
			break
		}
	}
	s := b.String()
	if len(s) > maxSummarizeInputCharsTurn {
		s = s[len(s)-maxSummarizeInputCharsTurn:]
	}
	return s
}

// memorySummarizeFunc produces an updated plain-text summary from a transcript and optional prior summary.
type memorySummarizeFunc func(ctx context.Context, model string, transcript string, previousSummary string) (string, error)

type asyncTurnMemory struct {
	ctx    context.Context
	logger *zap.Logger

	mu                sync.Mutex
	turns             []llmMemoryMessage
	summary           string
	maxMemoryMessages int

	summarizing  atomic.Bool
	summarizeSeq uint64
}

func newAsyncTurnMemory(ctx context.Context, logger *zap.Logger) *asyncTurnMemory {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &asyncTurnMemory{
		ctx:               ctx,
		logger:            logger,
		maxMemoryMessages: defaultMaxMemoryMessages,
	}
}

func (m *asyncTurnMemory) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns = m.turns[:0]
	m.summary = ""
}

func (m *asyncTurnMemory) setMaxMemoryMessages(n int) {
	if n <= 0 {
		n = defaultMaxMemoryMessages
	}
	m.mu.Lock()
	m.maxMemoryMessages = n
	if len(m.turns) > n {
		m.turns = m.turns[len(m.turns)-n:]
	}
	m.mu.Unlock()
}

func (m *asyncTurnMemory) getMaxMemoryMessages() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maxMemoryMessages <= 0 {
		return defaultMaxMemoryMessages
	}
	return m.maxMemoryMessages
}

func (m *asyncTurnMemory) mergedSystemPrompt(base string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := strings.TrimSpace(base)
	sum := strings.TrimSpace(m.summary)
	if sum != "" {
		if out != "" {
			out += "\n\n"
		}
		out += "Conversation summary so far: " + sum
	}
	return out
}

func (m *asyncTurnMemory) buildChatCompletionMessages(systemPrompt string, userText string) []map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := make([]map[string]string, 0, len(m.turns)+3)
	combined := strings.TrimSpace(systemPrompt)
	sum := strings.TrimSpace(m.summary)
	if sum != "" {
		if combined != "" {
			combined += "\n\n"
		}
		combined += "Conversation summary so far: " + sum
	}
	if combined != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": combined})
	}
	for _, t := range m.turns {
		msgs = append(msgs, map[string]string{"role": t.Role, "content": t.Content})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": userText})
	return msgs
}

func (m *asyncTurnMemory) snapshotTurns() []llmMemoryMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]llmMemoryMessage, len(m.turns))
	copy(out, m.turns)
	return out
}

func (m *asyncTurnMemory) summaryText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.TrimSpace(m.summary)
}

func (m *asyncTurnMemory) appendPairAndMaybeSummarize(ctx context.Context, model, userText, assistantText string, summarize memorySummarizeFunc) {
	m.mu.Lock()
	max := m.maxMemoryMessages
	if max <= 0 {
		max = defaultMaxMemoryMessages
	}
	m.turns = append(m.turns, llmMemoryMessage{Role: "user", Content: userText}, llmMemoryMessage{Role: "assistant", Content: assistantText})
	if len(m.turns) > max {
		m.turns = m.turns[len(m.turns)-max:]
	}
	localSummary := m.summary
	should := len(m.turns) >= max && !m.summarizing.Load()
	var snap []llmMemoryMessage
	var seq uint64
	if should && summarize != nil {
		m.summarizing.Store(true)
		seq = atomic.AddUint64(&m.summarizeSeq, 1)
		snap = make([]llmMemoryMessage, len(m.turns))
		copy(snap, m.turns)
		m.mu.Unlock()
		go m.runSummarize(ctx, model, snap, localSummary, seq, summarize)
		return
	}
	m.mu.Unlock()
}

func (m *asyncTurnMemory) runSummarize(ctx context.Context, model string, snapshot []llmMemoryMessage, previousSummary string, seq uint64, summarize memorySummarizeFunc) {
	transcript := buildTranscriptFromLLMMemory(snapshot)
	newSummary, err := summarize(ctx, model, transcript, previousSummary)
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.summarizing.Store(false)
	if seq != atomic.LoadUint64(&m.summarizeSeq) {
		return
	}
	if err != nil {
		m.logger.Warn("async memory summarization failed", zap.Error(err))
		return
	}
	newSummary = strings.TrimSpace(newSummary)
	if newSummary == "" {
		return
	}
	m.summary = newSummary
	m.compactAfterSummary(len(snapshot))
}

func (m *asyncTurnMemory) compactAfterSummary(snapshotLen int) {
	if snapshotLen <= 0 {
		m.turns = m.turns[:0]
		return
	}
	if len(m.turns) <= snapshotLen {
		m.turns = m.turns[:0]
		return
	}
	tail := make([]llmMemoryMessage, len(m.turns)-snapshotLen)
	copy(tail, m.turns[snapshotLen:])
	m.turns = tail
}

func (m *asyncTurnMemory) summarizeMemorySync(ctx context.Context, model string, summarize memorySummarizeFunc) (string, error) {
	if summarize == nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.summary, nil
	}
	m.mu.Lock()
	if len(m.turns) == 0 {
		s := m.summary
		m.mu.Unlock()
		return s, nil
	}
	if m.summarizing.Load() {
		m.mu.Unlock()
		return "", errors.New("summarization already in progress")
	}
	m.summarizing.Store(true)
	seq := atomic.AddUint64(&m.summarizeSeq, 1)
	prev := m.summary
	snap := make([]llmMemoryMessage, len(m.turns))
	copy(snap, m.turns)
	m.mu.Unlock()

	newSummary, err := summarize(ctx, model, buildTranscriptFromLLMMemory(snap), prev)
	newSummary = strings.TrimSpace(newSummary)

	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.summarizing.Store(false)
	if seq != atomic.LoadUint64(&m.summarizeSeq) {
		if err != nil {
			return "", err
		}
		return m.summary, nil
	}
	if err != nil {
		return "", err
	}
	if newSummary == "" {
		return m.summary, nil
	}
	m.summary = newSummary
	m.compactAfterSummary(len(snap))
	return m.summary, nil
}
