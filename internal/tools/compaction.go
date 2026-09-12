package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hirotomasato/hiroto/internal/llm"
)

// CompactionEngine orchestrates context compression using pluggable compressors.
type CompactionEngine struct {
	Config  CompactionConfig
	History []CompactionEntry
	router  *ModelRouter
	mu      sync.RWMutex
}

// NewCompactionEngine creates a compaction engine with the given config and compressor.
func NewCompactionEngine(config CompactionConfig, router *ModelRouter) *CompactionEngine {
	return &CompactionEngine{
		Config: config,
		router: router,
	}
}

// Compact is the main entry point: compresses the current context.
func (e *CompactionEngine) Compact(ctx context.Context, msgs []llm.Message) (*CompactionResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.Config.Enabled {
		return nil, fmt.Errorf("compaction disabled")
	}

	if e.Config.Method == "shake" {
		return e.compactShake(ctx, msgs)
	}
	if e.Config.Method == "llm" {
		return e.compactLLM(ctx, msgs)
	}
	return nil, fmt.Errorf("unknown compaction method: %q", e.Config.Method)
}

// AutoCheck checks if compaction is needed based on threshold.
func (e *CompactionEngine) AutoCheck(msgs []llm.Message) bool {
	tokens := estimateLLMTokens(msgs)
	return tokens > e.Config.ThresholdTokens
}

// TriggerReason returns why compaction was triggered.
func (e *CompactionEngine) TriggerReason(msgs []llm.Message) string {
	tokens := estimateLLMTokens(msgs)
	if tokens > e.Config.ThresholdTokens {
		return "threshold"
	}
	return "idle"
}

// Apply persists a compaction entry in the engine's history.
func (e *CompactionEngine) Apply(entry CompactionEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.History = append(e.History, entry)
}

// GetContext returns the current context messages and summary for LLM consumption.
func (e *CompactionEngine) GetContext(msgs []llm.Message) ([]llm.Message, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.History) == 0 {
		return msgs, ""
	}

	lastEntry := e.History[len(e.History)-1]
	// Start with the summary, then append the last messages.
	summary := lastEntry.Summary
	return msgs, summary
}

// PruneToolOutputs performs pre-compaction pruning: protects newest ~40k tokens,
// requires 20k savings, never blanks below 50 tokens.
func (e *CompactionEngine) PruneToolOutputs(msgs []llm.Message) []llm.Message {
	shake := NewShakeCompressor()
	_, _, err := shake.Compress(context.Background(), msgs, "")
	if err != nil {
		// Not enough savings; return original messages.
		return msgs
	}
	// Shake compressor already produced a result; discard it since we return
	// the modified messages slice (the compressor mutated msgs in place).
	return msgs
}

// compactShake runs shake compression.
func (e *CompactionEngine) compactShake(ctx context.Context, msgs []llm.Message) (*CompactionResult, error) {
	shake := NewShakeCompressor()
	method, result, err := shake.Compress(ctx, msgs, "")
	if err != nil {
		return nil, err
	}
	result.Method = method
	return &result, nil
}

// compactLLM runs LLM-based compression.
func (e *CompactionEngine) compactLLM(ctx context.Context, msgs []llm.Message) (*CompactionResult, error) {
	router := e.router
	if router == nil {
		return nil, fmt.Errorf("no model router configured for compaction")
	}

	// Convert llm.Message to LLMMessage.
	llmMsgs := make([]LLMMessage, len(msgs))
	for i, m := range msgs {
		llmMsgs[i] = llmMessageToLLMMessage(m)
	}

	compressor := NewLLMCompressor(router)
	method, result, err := compressor.Compress(ctx, llmMsgs, "")
	if err != nil {
		return nil, err
	}
	result.Method = method
	return &result, nil
}


// llmMessageToLLMMessage converts internal llm.Message to tools LLMMessage.
func llmMessageToLLMMessage(m llm.Message) LLMMessage {
	var content string
	if s, ok := m.Content.(string); ok {
		content = s
	}
	return LLMMessage{
		Role:    string(m.Role),
		Content: content,
	}
}

// BuildSummaryBlock formats messages as a readable transcript for the summariser LLM.
func BuildSummaryBlock(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			if s, ok := m.Content.(string); ok {
				b.WriteString(fmt.Sprintf("[USER] %s\n", s))
			}
		case llm.RoleAssistant:
			if s, ok := m.Content.(string); ok && s != "" {
				b.WriteString(fmt.Sprintf("[ASSISTANT] %s\n", s))
			}
			for _, tc := range m.ToolCalls {
				b.WriteString(fmt.Sprintf("[TOOL CALL] %s(%s)\n", tc.Function.Name, tc.Function.Arguments))
			}
		case llm.RoleTool:
			if s, ok := m.Content.(string); ok {
				short := s
				if len(short) > 300 {
					short = short[:300] + "…"
				}
				b.WriteString(fmt.Sprintf("[TOOL RESULT: %s] %s\n", m.Name, short))
			}
		case llm.RoleSystem:
			if s, ok := m.Content.(string); ok {
				b.WriteString(fmt.Sprintf("[SYSTEM] %s\n", s))
			}
		}
	}
	return b.String()
}
