package tools

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hirotomasato/hiroto/internal/llm"
)

// ShakeCompressor replaces large tool results with truncated markers
// while protecting the newest ~40k tokens and requiring at least 20k savings.
type ShakeCompressor struct {
	// NewestTokens protects this many tokens from the end.
	NewestTokens int
	// MinSavings is the minimum token reduction required to trigger compaction.
	MinSavings int
	// MinTokensAfter is the floor on remaining tokens to avoid blank contexts.
	MinTokensAfter int
}

// NewShakeCompressor creates a ShakeCompressor with standard defaults:
// protects newest 40k tokens, requires 20k savings, never blanks below 50 tokens.
func NewShakeCompressor() *ShakeCompressor {
	return &ShakeCompressor{
		NewestTokens:   40_000,
		MinSavings:     20_000,
		MinTokensAfter: 50,
	}
}

// Compress runs shake compression on the message history.
func (s *ShakeCompressor) Compress(ctx context.Context, msgs []llm.Message, prevSummary string) (string, CompactionResult, error) {
	totalTokens := estimateLLMTokens(msgs)
	protectedEnd := s.protectEndIndex(msgs, totalTokens)

	var kept []llm.Message
	var saved int
	for i, m := range msgs {
		if i >= protectedEnd {
			kept = append(kept, m)
			continue
		}
		if s.shouldProtect(m, prevSummary) {
			kept = append(kept, m)
			continue
		}

		content := messageContent(m)
		tokens := estimateTokensForContent(content)
		if tokens < s.MinTokensAfter {
			kept = append(kept, m)
			continue
		}

		truncated := fmt.Sprintf("[Output truncated - %d tokens]", tokens)
		m.Content = truncated
		kept = append(kept, m)
		saved += tokens
	}

	if saved < s.MinSavings {
		return "", CompactionResult{}, fmt.Errorf("shake compression saved only %d tokens (minimum %d required)", saved, s.MinSavings)
	}

	return "shake", CompactionResult{
		Method:       "shake",
		TokensBefore: totalTokens,
		TokensAfter:  estimateLLMTokens(kept),
		Summary:      fmt.Sprintf("Truncated large tool results; saved %d tokens", saved),
	}, nil
}

// protectEndIndex returns the index in msgs such that tokens from that index
// to the end equal roughly protectTokens.
func (s *ShakeCompressor) protectEndIndex(msgs []llm.Message, totalTokens int) int {
	protectFromEnd := totalTokens - s.NewestTokens
	if protectFromEnd <= 0 {
		return 0
	}

	cumulative := 0
	for i := range len(msgs) {
		content := messageContent(msgs[i])
		tokens := estimateTokensForContent(content)
		if cumulative+tokens >= protectFromEnd {
			return i
		}
		cumulative += tokens
	}
	return len(msgs)
}

// shouldProtect returns true if the message should never be truncated
// (skill results, skill:// reads, plan reference reads, system messages).
func (s *ShakeCompressor) shouldProtect(m llm.Message, prevSummary string) bool {
	_ = prevSummary // unused but accepted for interface compatibility
	switch m.Role {
	case llm.RoleSystem:
		return true
	}

	content := messageContent(m)
	if strings.HasPrefix(content, "[") && strings.Contains(content, "SKILL:") {
		return true
	}
	if strings.Contains(content, "skill://") || strings.Contains(content, "agent://") {
		return true
	}
	if strings.HasPrefix(content, "[RINGKASAN") {
		return true
	}
	return false
}

func messageContent(m llm.Message) string {
	if s, ok := m.Content.(string); ok {
		return s
	}
	return ""
}

func estimateTokensForContent(content string) int {
	if content == "" {
		return 0
	}
	runes := utf8.RuneCountInString(content)
	// ~3 runes per token for mixed Latin/Asian text
	return runes / 3
}

// estimateLLMTokens gives a rough token count for []llm.Message.
func estimateLLMTokens(msgs []llm.Message) int {
	tokens := 0
	for _, m := range msgs {
		content := messageContent(m)
		tokens += estimateTokensForContent(content)
		for _, tc := range m.ToolCalls {
			tokens += estimateTokensForContent(tc.Function.Name)
			tokens += estimateTokensForContent(tc.Function.Arguments)
		}
	}
	return tokens
}
