package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hirotomasato/hiroto/internal/llm"
)

// registerTask registers the `task` tool in the registry.
func registerTask(r *Registry, opts Options) {
	r.Register(&Tool{
		Name:        "task",
		Description: "Spawn a sub-agent as an isolated background task. Supports create, wait, result, output, status, cancel, send, and list actions. Use create to start a task with a goal, optional JSON schema for structured output, and list of tools to expose.",
		Parameters: mustJSON(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "description": "Action: create, wait, result, output, status, cancel, send, list", "enum": ["create", "wait", "result", "output", "status", "cancel", "send", "list"]},
				"goal": {"type": "string", "description": "The task goal/prompt (for create action)"},
				"id": {"type": "string", "description": "Task ID (for wait, result, output, status, cancel, send actions)"},
				"schema": {"type": "string", "description": "JSON Schema for output validation (create action)"},
				"tools": {"type": "string", "description": "Comma-separated list of tool names to expose (create action)"},
				"message": {"type": "string", "description": "Message to send to a running task (send action)"},
				"timeout": {"type": "integer", "description": "Max seconds to wait for task (wait action, default 300)"}
			},
			"required": ["action"]
		}`),
		Exec: func(ctx context.Context, args map[string]any) Result {
			return taskExec(ctx, args, opts)
		},
	})
}

// taskExec handles all task tool actions.
func taskExec(ctx context.Context, args map[string]any, opts Options) Result {
	action, _ := args["action"].(string)
	switch action {
	case "create":
		return taskCreate(ctx, args, opts)
	case "wait":
		return taskWait(args, opts)
	case "result":
		return taskResult(args, opts)
	case "output":
		return taskOutput(args, opts)
	case "status":
		return taskStatus(args, opts)
	case "cancel":
		return taskCancel(args, opts)
	case "send":
		return taskSend(args, opts)
	case "list":
		return taskList(opts)
	default:
		return Result{Output: fmt.Sprintf("unknown action: %q. Supported: create, wait, result, output, status, cancel, send, list", action), IsError: true}
	}
}

// taskCreate spawns a new sub-agent task.
func taskCreate(ctx context.Context, args map[string]any, opts Options) Result {
	goal, _ := args["goal"].(string)
	if goal == "" {
		return Result{Output: "missing goal", IsError: true}
	}

	schemaStr, _ := args["schema"].(string)
	var schema Schema
	if schemaStr != "" {
		schema = JSONToSchema(schemaStr)
		if schema == nil {
			return Result{Output: "invalid JSON schema", IsError: true}
		}
	}

	toolsStr, _ := args["tools"].(string)
	var tools []string
	if toolsStr != "" {
		for _, t := range strings.Split(toolsStr, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tools = append(tools, t)
			}
		}
	}

	tm := opts.TaskManager
	if tm == nil {
		return Result{Output: "task system not initialized", IsError: true}
	}

	id, err := tm.Create(goal, schema, tools)
	if err != nil {
		return Result{Output: fmt.Sprintf("create failed: %v", err), IsError: true}
	}

	// Launch the agent in a background goroutine.
	go func() {
		client := opts.LLMClient
		var llmClient *llm.Client
		if client != nil {
			// Cast the LLMClient to the concrete type.
			if adapter, ok := client.(*llmAdapter); ok {
				llmClient = adapter.client
			}
		}

		reg := opts.Reg
		if reg != nil {
			// Filter registry to only expose selected tools.
			filteredReg := NewRegistry()
			for _, tn := range tools {
				if t, ok := reg.Get(tn); ok {
					filteredReg.Register(t)
				}
			}
			reg = filteredReg
		}

		err := runAgent(ctx, goal, schema, tools, tm, id, llmClient, opts.Workdir, reg, opts.EmitFunc)
		if err != nil {
			// runAgent already sets the final status.
		}
	}()

	return Result{Output: fmt.Sprintf("Task %s created (status: pending). Use task(id=\"%s\", action=\"wait\") to poll for completion.", id, id)}
}

// taskWait blocks until the task completes or times out.
func taskWait(args map[string]any, opts Options) Result {
	id, _ := args["id"].(string)
	if id == "" {
		return Result{Output: "missing id", IsError: true}
	}

	tm := opts.TaskManager
	if tm == nil {
		return Result{Output: "task system not initialized", IsError: true}
	}

	timeout := 300
	if t, ok := toInt(args["timeout"]); ok && t > 0 {
		timeout = t
	}

	// Create a context with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		tm.WaitFor(id)
		close(done)
	}()

	select {
	case <-done:
		t, _ := tm.Get(id)
		if t != nil {
			t.mu.RLock()
			defer t.mu.RUnlock()
			return Result{
				Output: fmt.Sprintf("Task %s completed (status: %s)\n\nResult:\n%s", id, t.Status, t.Result),
			}
		}
	case <-ctx.Done():
		return Result{Output: fmt.Sprintf("Task %s timed out after %ds", id, timeout), IsError: true}
	}
	return Result{Output: fmt.Sprintf("Task %s completed", id)}
}

// taskResult returns the final text result.
func taskResult(args map[string]any, opts Options) Result {
	id, _ := args["id"].(string)
	if id == "" {
		return Result{Output: "missing id", IsError: true}
	}

	tm := opts.TaskManager
	if tm == nil {
		return Result{Output: "task system not initialized", IsError: true}
	}

	t, err := tm.Result(id)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}
	}

	return Result{Output: t.Result}
}

// taskOutput returns the parsed JSON output.
func taskOutput(args map[string]any, opts Options) Result {
	id, _ := args["id"].(string)
	if id == "" {
		return Result{Output: "missing id", IsError: true}
	}

	tm := opts.TaskManager
	if tm == nil {
		return Result{Output: "task system not initialized", IsError: true}
	}

	output, err := tm.Output(id)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}
	}

	b, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return Result{Output: fmt.Sprintf("marshal error: %v", err), IsError: true}
	}
	return Result{Output: string(b)}
}

// taskStatus returns the current status.
func taskStatus(args map[string]any, opts Options) Result {
	id, _ := args["id"].(string)
	if id == "" {
		return Result{Output: "missing id", IsError: true}
	}

	tm := opts.TaskManager
	if tm == nil {
		return Result{Output: "task system not initialized", IsError: true}
	}

	t, ok := tm.Get(id)
	if !ok {
		return Result{Output: fmt.Sprintf("task %s not found", id), IsError: true}
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	return Result{Output: fmt.Sprintf("Task %s: %s", id, t.Status)}
}

// taskCancel cancels a running task.
func taskCancel(args map[string]any, opts Options) Result {
	id, _ := args["id"].(string)
	if id == "" {
		return Result{Output: "missing id", IsError: true}
	}

	tm := opts.TaskManager
	if tm == nil {
		return Result{Output: "task system not initialized", IsError: true}
	}

	if err := tm.Cancel(id); err != nil {
		return Result{Output: err.Error(), IsError: true}
	}
	return Result{Output: fmt.Sprintf("Task %s cancelled", id)}
}

// taskSend sends a message to a running task.
func taskSend(args map[string]any, opts Options) Result {
	id, _ := args["id"].(string)
	if id == "" {
		return Result{Output: "missing id", IsError: true}
	}
	message, _ := args["message"].(string)
	if message == "" {
		return Result{Output: "missing message", IsError: true}
	}

	tm := opts.TaskManager
	if tm == nil {
		return Result{Output: "task system not initialized", IsError: true}
	}

	t, ok := tm.Get(id)
	if !ok {
		return Result{Output: fmt.Sprintf("task %s not found", id), IsError: true}
	}

	t.mu.RLock()
	steerCh := t.SteerCh
	status := t.Status
	t.mu.RUnlock()

	if status != StatusRunning {
		return Result{Output: fmt.Sprintf("task %s is %s, not running", id, status), IsError: true}
	}

	if steerCh == nil {
		return Result{Output: fmt.Sprintf("task %s has no steering channel", id), IsError: true}
	}

	select {
	case steerCh <- message:
		return Result{Output: fmt.Sprintf("Message sent to task %s", id)}
	default:
		return Result{Output: fmt.Sprintf("task %s steering channel full", id), IsError: true}
	}
}

// taskList returns all tasks.
func taskList(opts Options) Result {
	tm := opts.TaskManager
	if tm == nil {
		return Result{Output: "task system not initialized", IsError: true}
	}

	tasks := tm.List()
	if len(tasks) == 0 {
		return Result{Output: "no tasks"}
	}

	var lines []string
	for _, t := range tasks {
		t.mu.RLock()
		lines = append(lines, fmt.Sprintf("  %s: %s (%s)", t.ID, t.Name, t.Status))
		t.mu.RUnlock()
	}
	return Result{Output: fmt.Sprintf("%d task(s):\n%s", len(tasks), strings.Join(lines, "\n"))}
}

// llmAdapter bridges tools.LLMClient to the internal LLM client.
// Defined here so task_tool.go can access it without import cycle.
type llmAdapter struct {
	client *llm.Client
}

// Chat implements tools.LLMClient.
func (a *llmAdapter) Chat(ctx context.Context, messages []LLMMessage) (string, error) {
	if a.client == nil {
		return "", fmt.Errorf("LLM client not configured")
	}
	msgs := make([]llm.Message, len(messages))
	for i, m := range messages {
		msgs[i] = llm.Message{
			Role:    llm.Role(m.Role),
			Content: m.Content,
		}
	}
	resp, err := a.client.Chat(ctx, msgs, nil)
	if err != nil {
		return "", err
	}
	content, _ := resp.Content.(string)
	return content, nil
}
