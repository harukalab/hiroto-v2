package tools

import (
	"context"
	"fmt"
	"strings"
)

// LLMCompressor delegates compaction to the slow model via the ModelRouter.
type LLMCompressor struct {
	Router *ModelRouter
}

// NewLLMCompressor creates a compressor that uses the slow model for summarisation.
func NewLLMCompressor(router *ModelRouter) *LLMCompressor {
	return &LLMCompressor{Router: router}
}

// Compress calls the LLM with a compact summary prompt, producing a
// summary that replaces the oldest messages. It handles split-turn compaction
// by emitting a history summary block plus a turn-prefix message.
func (l *LLMCompressor) Compress(ctx context.Context, msgs []LLMMessage, prevSummary string) (string, CompactionResult, error) {
	if len(msgs) == 0 {
		return "", CompactionResult{}, fmt.Errorf("no messages to compress")
	}

	toSummarize := msgs[:len(msgs)-4] // keep last 4 messages
	if len(toSummarize) == 0 {
		return "", CompactionResult{}, fmt.Errorf("not enough messages to compress")
	}

	summaryBlock := buildCompactionBlock(toSummarize)

	// Build the system prompt for compaction summarisation.
	systemPrompt := buildCompactionSystemPrompt(prevSummary)

	// Resolve the slow model client.
	client, err := l.Router.Resolve(RoleSlow)
	if err != nil {
		// Fallback: try default role.
		client, err = l.Router.Resolve(RoleDefault)
		if err != nil {
			return "", CompactionResult{}, fmt.Errorf("no LLM route for compaction: %w", err)
		}
	}

	resp, err := client.Chat(ctx, []LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: summaryBlock},
	})
	if err != nil {
		return "", CompactionResult{}, fmt.Errorf("LLM compaction failed: %w", err)
	}

	summary := resp
	before := estimateTokensForMsgs(msgs)
	after := before - estimateTokensForContent(summaryBlock) + estimateTokensForContent(summary)

	return "llm", CompactionResult{
		Method:       "llm",
		TokensBefore: before,
		TokensAfter:  after,
		Summary:      summary,
	}, nil
}

// buildCompactionSystemPrompt returns the system prompt for the summariser LLM.
// If prevSummary is non-empty, it produces an update prompt; otherwise a fresh summary prompt.
func buildCompactionSystemPrompt(prevSummary string) string {
	if prevSummary != "" {
		return `Anda adalah peringkas percakapan untuk konteks kompresi agen AI.
Ringkaskan percakapan berikut dan update ringkasan yang sudah ada:
- Pertahankan SEMUA informasi penting dari kedua ringkasan
- Gabungkan temuan baru, keputusan, dan action items
- Buang duplikasi dan informasi yang sudah tidak relevan
- Gunakan format bullet-point atau paragraf padat.

RINGKASAN SEBELUMNYA:
` + prevSummary + `

PEMBAHARUAN PERCAKAPAN:
`
	}
	return `Anda adalah peringkas percakapan untuk konteks kompresi agen AI.
Ringkas percakapan berikut dengan mempertahankan SEMUA informasi penting:
- Keputusan yang dibuat
- Kode snippet dan tujuannya
- Action items dan statusnya
- Fakta dan informasi yang ditemukan
- Preferensi dan feedback pengguna
- Tools yang dipakai dan hasilnya

Output ringkasan singkat dalam bahasa yang sama dengan percakapan.
Gunakan format bullet-point atau paragraf padat.`
}

// buildCompactionBlock formats messages for the summariser LLM.
func buildCompactionBlock(msgs []LLMMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			b.WriteString(fmt.Sprintf("[USER] %s\n", m.Content))
		case "assistant":
			b.WriteString(fmt.Sprintf("[ASSISTANT] %s\n", m.Content))
		case "tool":
			b.WriteString(fmt.Sprintf("[TOOL RESULT] %s\n", m.Content))
		case "system":
			b.WriteString(fmt.Sprintf("[SYSTEM] %s\n", m.Content))
		}
	}
	return b.String()
}

// estimateTokensForMsgs rough token count for []LLMMessage.
func estimateTokensForMsgs(msgs []LLMMessage) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokensForContent(m.Content)
	}
	return total
}
