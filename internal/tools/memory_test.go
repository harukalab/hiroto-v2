package tools

import (
	"context"
	"sync"
	"testing"

	"github.com/hirotomasato/hiroto/internal/session"
)

// mockLLMClient is a simple LLM mock for testing.
type mockLLMClient struct {
	mu    sync.Mutex
	calls []string
}

func (m *mockLLMClient) Chat(ctx context.Context, messages []LLMMessage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Echo the last message content, prefixed with "LLM: "
	lastMsg := messages[len(messages)-1]
	m.calls = append(m.calls, lastMsg.Content)
	return "LLM: extracted durable knowledge from session", nil
}

func TestMemoryPipelineExtract(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	sess := &session.Session{
		ID:      "test-session-1",
		Title:   "Test Session",
		Messages: []session.StoredMessage{
			{Role: "user", Content: "Hello world"},
			{Role: "assistant", Content: "Hi there!"},
		},
	}

	err := pipeline.Extract(sess)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if pipeline.entryCount() != 1 {
		t.Fatalf("expected 1 entry, got %d", pipeline.entryCount())
	}

	// Verify the entry was created
	pipeline.mu.RLock()
	entry := pipeline.Entries[0]
	pipeline.mu.RUnlock()

	if entry.SourceSession != "test-session-1" {
		t.Errorf("expected SourceSession test-session-1, got %s", entry.SourceSession)
	}
	if entry.Phase != 1 {
		t.Errorf("expected Phase 1, got %d", entry.Phase)
	}
}

func TestMemoryPipelineConsolidate(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	sess := &session.Session{
		ID:      "test-session-2",
		Title:   "Another Session",
		Messages: []session.StoredMessage{
			{Role: "user", Content: "Test message"},
		},
	}

	err := pipeline.Extract(sess)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	err = pipeline.Consolidate()
	if err != nil {
		t.Fatalf("Consolidate failed: %v", err)
	}

	consolidated := pipeline.GetConsolidated()
	if consolidated == "" {
		t.Fatal("consolidated output is empty")
	}
}

func TestMemoryPipelineClear(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	sess := &session.Session{
		ID:       "clear-test",
		Title:    "Clear Test",
		Messages: []session.StoredMessage{{Role: "user", Content: "test"}},
	}

	pipeline.Extract(sess)
	if pipeline.entryCount() != 1 {
		t.Fatal("expected 1 entry before clear")
	}

	pipeline.Clear()
	if pipeline.entryCount() != 0 {
		t.Fatal("expected 0 entries after clear")
	}
	if pipeline.GetConsolidated() != "" {
		t.Fatal("expected empty consolidated after clear")
	}
}

func TestMemoryPipelineInject(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	sess := &session.Session{
		ID:       "inject-test",
		Title:    "Inject Test",
		Messages: []session.StoredMessage{{Role: "user", Content: "test"}},
	}

	pipeline.Extract(sess)
	pipeline.Consolidate()

	inject := pipeline.InjectIntoPrompt()
	if inject == "" {
		t.Fatal("inject output is empty")
	}
	if inject != "## Consolidated Memory\n"+pipeline.GetConsolidated() {
		t.Fatalf("unexpected inject output: %s", inject)
	}
}

func TestMemoryPipelineEnqueue(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	pipeline.Enqueue("session-abc")
	pipeline.Enqueue("session-def")

	// Both sessions queued
	if pipeline.queuedCount() != 2 {
		t.Fatalf("expected 2 queued, got %d", pipeline.queuedCount())
	}

	// Extract session-abc, it should be removed from queue
	sess1 := &session.Session{
		ID:       "session-abc",
		Title:    "ABC",
		Messages: []session.StoredMessage{{Role: "user", Content: "test"}},
	}
	pipeline.Extract(sess1)

	if pipeline.queuedCount() != 1 {
		t.Fatalf("expected 1 queued after extract, got %d", pipeline.queuedCount())
	}
	if pipeline.entryCount() != 1 {
		t.Fatalf("expected 1 entry after extract, got %d", pipeline.entryCount())
	}
}

func TestMemoryExecClear(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	// Add a session
	sess := &session.Session{
		ID:       "exec-test",
		Title:    "Exec Test",
		Messages: []session.StoredMessage{{Role: "user", Content: "test"}},
	}
	pipeline.Extract(sess)
	if pipeline.entryCount() != 1 {
		t.Fatal("expected 1 entry")
	}

	result := memoryExecClear(pipeline)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if pipeline.entryCount() != 0 {
		t.Fatal("expected 0 entries after clear")
	}
}

func TestMemoryExecGetEmpty(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	result := memoryExecGet(pipeline)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
}

func TestMemoryExecEnqueue(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	result := memoryExecEnqueue("session-xyz", pipeline)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if pipeline.queuedCount() != 1 {
		t.Fatal("expected 1 queued session")
	}
}

func TestMemoryExecInject(t *testing.T) {
	llm := &mockLLMClient{}
	backend := NewLocalMemoryBackend(llm, nil)
	pipeline := NewMemoryPipeline(backend)

	sess := &session.Session{
		ID:       "inject-exec",
		Title:    "Exec Inject",
		Messages: []session.StoredMessage{{Role: "user", Content: "test"}},
	}
	pipeline.Extract(sess)
	pipeline.Consolidate()

	result := memoryExecInject(pipeline)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if result.Output == "" {
		t.Fatal("inject output is empty")
	}
}

func TestExtractNilSession(t *testing.T) {
	backend := NewLocalMemoryBackend(nil, nil)
	pipeline := NewMemoryPipeline(backend)

	err := pipeline.Extract(nil)
	if err == nil {
		t.Fatal("expected error for nil session")
	}
	if err.Error() != "nil session" {
		t.Fatalf("unexpected error: %v", err)
	}
}
