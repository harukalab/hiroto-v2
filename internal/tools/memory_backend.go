package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hirotomasato/hiroto/internal/memory"
	"github.com/hirotomasato/hiroto/internal/session"
)

const (
	memoryDirName  = "memory"
	memoryFile     = "MEMORY.md"
	summaryFile    = "memory_summary.md"
	skillsDirName  = "skills"
)

// LocalMemoryBackend implements MemoryBackend using the LLM client and local files.
type LocalMemoryBackend struct {
	llm     LLMClient
	memory  *memory.Store
	homeDir string
}

// NewLocalMemoryBackend creates a backend using the given LLM client and memory store.
func NewLocalMemoryBackend(llm LLMClient, mem *memory.Store) *LocalMemoryBackend {
	home := defaultHomeDir()
	if mem != nil && mem.Dir != "" {
		home = mem.Dir
	}
	if err := os.MkdirAll(filepath.Join(home, memoryDirName, skillsDirName), 0o755); err != nil {
		return nil
	}
	return &LocalMemoryBackend{
		llm:     llm,
		memory:  mem,
		homeDir: home,
	}
}

// Extract produces a MemoryEntry from a session by asking the LLM to summarize its content.
func (b *LocalMemoryBackend) Extract(s *session.Session) (*MemoryEntry, error) {
	if b.llm == nil {
		return nil, fmt.Errorf("LLM client not configured for extraction")
	}
	if s == nil || len(s.Messages) == 0 {
		return nil, fmt.Errorf("empty session %s", s.ID)
	}

	// Build extraction prompt from session messages.
	sessionText := buildSessionText(s)
	prompt := fmt.Sprintf(extractionPrompt, s.ID, s.Title, sessionText)

	resp, err := b.llm.Chat(context.Background(), []LLMMessage{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM extraction for session %s: %w", s.ID, err)
	}

	// Parse LLM response into Content and Synopsis.
	content, synopsis := parseExtractionResponse(resp)

	return &MemoryEntry{
		ID:            fmt.Sprintf("mem_%s_%d", s.ID, len(s.Messages)),
		Content:       content,
		Synopsis:      synopsis,
		SourceSession: s.ID,
		Phase:         1,
		CreatedAt:     time.Now(),
	}, nil
}

// Consolidate synthesizes a consolidated memory text from all entries using the LLM.
func (b *LocalMemoryBackend) Consolidate(entries []*MemoryEntry) (string, error) {
	if b.llm == nil {
		return "", fmt.Errorf("LLM client not configured for consolidation")
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no entries to consolidate")
	}

	// Build consolidation prompt with all entries.
	var sb strings.Builder
	sb.WriteString("Consolidate the following memory entries into a single coherent summary:\n\n")
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("### [%s] %s\n", e.SourceSession, e.Synopsis))
		sb.WriteString(e.Content)
		sb.WriteString("\n\n")
	}

	resp, err := b.llm.Chat(context.Background(), []LLMMessage{
		{Role: "user", Content: sb.String() + "\nProduce a consolidated memory summary:"},
	})
	if err != nil {
		return "", fmt.Errorf("LLM consolidation: %w", err)
	}

	// Write consolidated output to files.
	b.writeConsolidated(resp)

	return resp, nil
}

// writeConsolidated saves consolidated memory to disk.
func (b *LocalMemoryBackend) writeConsolidated(content string) {
	memoryDir := filepath.Join(b.homeDir, memoryDirName)
	_ = os.MkdirAll(memoryDir, 0o755)

	_ = os.WriteFile(filepath.Join(memoryDir, memoryFile), []byte(content), 0o644)
	_ = os.WriteFile(filepath.Join(memoryDir, summaryFile), []byte(content), 0o644)
}

// buildSessionText creates a text representation of session messages.
func buildSessionText(s *session.Session) string {
	var sb strings.Builder
	for _, m := range s.Messages {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", m.Role, m.Content))
	}
	return sb.String()
}

// parseExtractionResponse splits the LLM response into content and synopsis.
func parseExtractionResponse(resp string) (content, synopsis string) {
	content = strings.TrimSpace(resp)
	synopsis = content
	// Use first line as synopsis if available.
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) > 0 {
		synopsis = strings.TrimSpace(lines[0])
		if len(synopsis) > 200 {
			synopsis = synopsis[:200] + "..."
		}
	}
	return content, synopsis
}

// extractionPrompt is the system prompt used to extract memories from a session.
const extractionPrompt = `Session: %s — "%s"

Extract durable knowledge from this session. Focus on:
- Code patterns and conventions discovered
- User preferences and work style
- Environment details and tool configurations
- Key decisions and their rationale
- Skills or capabilities identified

Session transcript:
%s

Provide a concise summary followed by detailed findings.`

// defaultHomeDir returns the Hiroto home directory.
func defaultHomeDir() string {
	if h := os.Getenv("HIROTO_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".hiroto")
}
