package tools

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Event is streamed to the UI: text chunks, tool activity, and errors.
type Event struct {
	Type     string        // "text", "tool_start", "tool_end", "turn", "error", "done"
	Text     string
	ToolName string
	ToolArgs string
	Duration time.Duration
}
// TaskStatus is the lifecycle state of a sub-agent task.
// Uses todo.go constants: StatusPending, StatusInProgress, StatusCompleted, StatusCancelled.
type TaskStatus string

// StatusRunning indicates a task is actively executing.
const StatusRunning TaskStatus = "running"

// StatusFailed indicates a task completed with an error.
const StatusFailed TaskStatus = "failed"
// Task represents a sub-agent spawned by the task tool.
type Task struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Prompt    string            `json:"prompt"`
	Schema    map[string]any    `json:"schema,omitempty"`
	Tools     []string          `json:"tools,omitempty"`
	Status    TaskStatus        `json:"status"`
	Result    string            `json:"result,omitempty"`
	Output    map[string]any    `json:"output,omitempty"`
	Err       string            `json:"error,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
	done      chan struct{} // closed when task finishes
	mu        sync.RWMutex  // guards Result, Output, Err, Status
	SteerCh   chan string    // injected mid-turn: read after each tool call
}

// TaskManager manages the lifecycle of sub-agent tasks.
type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

// NewTaskManager creates an empty task manager.
func NewTaskManager() *TaskManager {
	return &TaskManager{tasks: make(map[string]*Task)}
}

// Create registers a new task and returns its ID. The task starts in pending state.
func (tm *TaskManager) Create(prompt string, schema map[string]any, tools []string) (string, error) {
	id := generateUUID()
	t := &Task{
		ID:        id,
		Name:      prompt,
		Prompt:    prompt,
		Schema:    schema,
		Tools:     tools,
		Status:    StatusPending,
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}
	tm.mu.Lock()
	tm.tasks[id] = t
	tm.mu.Unlock()
	return id, nil
}

// Get returns a task by ID.
func (tm *TaskManager) Get(id string) (*Task, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	t, ok := tm.tasks[id]
	return t, ok
}

// Cancel transitions a pending or running task to cancelled.
func (tm *TaskManager) Cancel(id string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	t, ok := tm.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	if t.Status == StatusCompleted || t.Status == StatusCancelled {
		return fmt.Errorf("task %s already %s", id, t.Status)
	}
	t.Status = StatusCancelled
	return nil
}

// Result returns the final text result of a completed task.
func (tm *TaskManager) Result(id string) (*Task, error) {
	tm.mu.RLock()
	t, ok := tm.tasks[id]
	tm.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.Status != StatusCompleted {
		return nil, fmt.Errorf("task %s is %s", id, t.Status)
	}
	return t, nil
}

// Output returns the parsed JSON output of a completed task.
func (tm *TaskManager) Output(id string) (map[string]any, error) {
	tm.mu.RLock()
	t, ok := tm.tasks[id]
	tm.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.Status != StatusCompleted {
		return nil, fmt.Errorf("task %s is %s", id, t.Status)
	}
	return t.Output, nil
}

// List returns all tasks.
func (tm *TaskManager) List() []*Task {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]*Task, 0, len(tm.tasks))
	for _, t := range tm.tasks {
		out = append(out, t)
	}
	return out
}

// WaitFor blocks until the task reaches a terminal status (completed, failed, cancelled).
func (tm *TaskManager) WaitFor(id string) error {
	tm.mu.RLock()
	t, ok := tm.tasks[id]
	tm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	<-t.done
	return nil
}

// setStatus sets the task status and closes the done channel if terminal.
func (t *Task) setStatus(s TaskStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = s
	if s == StatusCompleted || s == StatusFailed || s == StatusCancelled {
		now := time.Now()
		t.CompletedAt = &now
		close(t.done)
	}
}

// setFinalResult sets the result and output of a completed task.
func (t *Task) setFinalResult(result string, output map[string]any, err string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Result = result
	t.Output = output
	t.Err = err
	if t.Status == StatusRunning {
		if err != "" {
			t.Status = StatusFailed
		} else {
			t.Status = StatusCompleted
		}
		now := time.Now()
		t.CompletedAt = &now
		close(t.done)
	}
}

// generateUUID generates a simple UUID v4-like string.
func generateUUID() string {
	// Use a fast, simple UUID-like format: 8-4-4-4-12 hex digits.
	// This avoids importing github.com/google/uuid.
	b := make([]byte, 36)
	const hex = "0123456789abcdef"
	for i := 0; i < 36; i++ {
		switch i {
		case 8, 13, 18, 23:
			b[i] = '-'
		default:
			b[i] = hex[time.Now().UnixNano()%16]
		}
	}
	return string(b)
}

// Schema represents a JSON Schema (subset) used for validating task output.
type Schema map[string]any

// ValidateResult checks that the parsed output conforms to the schema.
// Returns an error if validation fails, nil if it passes.
func (s Schema) ValidateResult(result any) error {
	if s == nil || len(s) == 0 {
		return nil // no schema = no validation
	}

	schemaType, _ := s["type"].(string)
	if schemaType == "" {
		return nil // no type constraint
	}

	switch schemaType {
	case "object":
		m, ok := result.(map[string]any)
		if !ok {
			return fmt.Errorf("expected object, got %T", result)
		}
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if name, ok := r.(string); ok {
					if _, has := m[name]; !has {
						return fmt.Errorf("missing required field: %s", name)
					}
				}
			}
		}
		if props, ok := s["properties"].(map[string]any); ok {
			for name, def := range props {
				prop, ok := def.(map[string]any)
				if !ok {
					continue
				}
				val, has := m[name]
				required, _ := s["required"].([]any)
				isRequired := false
				if has {
					for _, r := range required {
						if rn, ok := r.(string); ok && rn == name {
							isRequired = true
							break
						}
					}
				}
				if !has && isRequired {
					return fmt.Errorf("missing required field: %s", name)
				}
				if has && prop["type"] != nil {
					if err := Schema(prop).ValidateResult(val); err != nil {
						return fmt.Errorf("field %s: %w", name, err)
					}
				}
			}
		}
	case "string":
		if _, ok := result.(string); !ok {
			return fmt.Errorf("expected string, got %T", result)
		}
	case "integer":
		if _, ok := result.(float64); !ok {
			return fmt.Errorf("expected integer, got %T", result)
		}
	case "number":
		if _, ok := result.(float64); !ok {
			return fmt.Errorf("expected number, got %T", result)
		}
	case "boolean":
		if _, ok := result.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", result)
		}
	case "array":
		if _, ok := result.([]any); !ok {
			return fmt.Errorf("expected array, got %T", result)
		}
	}
	return nil
}

// parseJSONArgs safely parses a JSON string into a map[string]any.
func parseJSONArgs(s string) map[string]any {
	var result map[string]any
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil
	}
	return result
}

// parseJSONValue safely unmarshals JSON into an any value.
func parseJSONValue(s string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// SchemaToJSON serializes a Schema to a compact JSON string.
func SchemaToJSON(s Schema) string {
	b, err := json.Marshal(s)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// JSONToSchema parses a JSON schema string into a Schema map.
func JSONToSchema(s string) Schema {
	var m map[string]any
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

// MarshalJSON implements json.Marshaler for Schema (handles nil).
func (s Schema) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	return json.Marshal(map[string]any(s))
}

// UnmarshalJSON implements json.Unmarshaler for Schema.
func (s *Schema) UnmarshalJSON(data []byte) error {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*s = m
	return nil
}

// ToolCall represents a function call from the agent to a tool.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolResult is the result of a tool execution.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}
