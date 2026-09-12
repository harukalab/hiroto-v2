package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hirotomasato/hiroto/internal/llm"
)
// runAgent executes an agent loop in the caller's goroutine. It calls the LLM,
// processes tool calls, and stores the final result in the task.
//
// The agent loop is:
//   1. Send system prompt + user prompt + existing messages
//   2. If LLM returns tool calls: execute tools, collect results, loop to step 1
//   3. If LLM returns text: validate against schema (if present), store result
//
// The context is used for per-LLM-call timeouts. If the task is cancelled
// via TaskManager.Cancel, the loop checks task.done after each tool call.
func runAgent(ctx context.Context, prompt string, schema Schema, tools []string, tm *TaskManager, taskID string, llmClient *llm.Client, workdir string, reg *Registry, emit func(Event)) error {
	// Look up the task.
	t, ok := tm.Get(taskID)
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	t.setStatus(StatusRunning)
	// Build the messages for this agent.
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}
	// Collect available tool definitions.
	var toolDefs []llm.ToolDefinition
	if reg != nil {
		for _, name := range tools {
			if name == "" {
				continue
			}
			// Only include tools whose names are in the tools list.
			// If tools is empty, include all available tools.
			tool, ok := reg.Get(name)
			if !ok {
				continue
			}
			toolDefs = append(toolDefs, llm.ToolDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			})
		}
	} else if len(tools) > 0 {
		// No registry but tools specified — use defaults for those.
		toolDefs = defaultToolDefs(tools)
	}
	// Agent loop with per-call timeout.
	maxTurns := 15 // max LLM calls per task
	for turn := 0; turn < maxTurns; turn++ {
		// Check if task was cancelled.
		t.mu.RLock()
		cancelled := t.Status == StatusCancelled
		t.mu.RUnlock()
		if cancelled {
			t.setFinalResult("cancelled", nil, "task was cancelled")
			return nil
		}
		// Per-call timeout: 60 seconds.
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		resp, err := llmClient.Chat(callCtx, messages, wrapLLMTools(toolDefs))
		cancel()
		if err != nil {
			// Check if context was cancelled.
			if callCtx.Err() == context.Canceled {
				t.setFinalResult("cancelled", nil, "task was cancelled")
				return nil
			}
			t.setFinalResult("", nil, fmt.Sprintf("LLM call failed: %v", err))
			return fmt.Errorf("LLM call: %w", err)
		}
		// Check for tool calls.
		if len(resp.ToolCalls) > 0 {
			// Emit tool_start for each.
			for _, tc := range resp.ToolCalls {
				if emit != nil {
					emit(Event{Type: "tool_start", ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments})
				}
			}
			// Execute tools.
			results := executeToolCalls(ctx, reg, resp.ToolCalls)
			// Emit tool_end for each.
			for i, tc := range resp.ToolCalls {
				if emit != nil {
					emit(Event{Type: "tool_end", ToolName: tc.Function.Name, ToolArgs: tc.Function.Arguments, Text: results[i].Content})
				}
			}
			// Append assistant message and tool results.
			messages = append(messages, llm.Message{
				Role:      llm.RoleAssistant,
				ToolCalls: resp.ToolCalls,
			})
			for _, tr := range results {
				messages = append(messages, llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: tr.ToolCallID,
					Name:       tr.Name,
					Content:    tr.Content,
				})
			}
		// Inject any mid-turn steer message.
		t.mu.RLock()
		steerCh := t.SteerCh
		t.mu.RUnlock()
		if steerCh != nil {
			select {
			case msg := <-steerCh:
				messages = append(messages, llm.Message{
					Role:    llm.RoleUser,
					Content: fmt.Sprintf("Injected message: %s", msg),
				})
			default:
			}
		}
		continue
	}
	content, _ := resp.Content.(string)
	// No tool calls — this is the final answer.
	if content == "" {
		content = "(no response)"
	}
	// Validate against schema.
		if schema != nil {
			var parsed any
			if err := json.Unmarshal([]byte(content), &parsed); err == nil {
				if err := schema.ValidateResult(parsed); err != nil {
					// Re-attempt with validation error.
					messages = append(messages, llm.Message{
						Role:    llm.RoleUser,
						Content: fmt.Sprintf("Your previous response did not match the required schema: %v. Please try again:\n%s", err, content),
					})
					continue
				}
			}
		}
		// Parse output if it's valid JSON.
		var output map[string]any
		if json.Unmarshal([]byte(content), &output) == nil {
			// Also validate against schema for structured output.
			if schema != nil {
				if verr := schema.ValidateResult(output); verr != nil {
					// Still accept it but note the issue.
					output["_schema_warning"] = verr.Error()
				}
			}
		}
		t.setFinalResult(content, output, "")
		return nil
	}
	// Max turns exceeded.
	t.setFinalResult("", nil, fmt.Sprintf("max turns (%d) exceeded", maxTurns))
	return fmt.Errorf("max turns exceeded")
}
// executeToolCalls executes tool calls from the agent, using the registry or
// the hiroto subprocess fallback.
func executeToolCalls(ctx context.Context, reg *Registry, calls []llm.ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))
	for i, tc := range calls {
		if reg != nil {
			tool, ok := reg.Get(tc.Function.Name)
			if ok {
				result := tool.Exec(ctx, JSONArgs(tc.Function.Arguments))
				results[i] = ToolResult{
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    result.Output,
					IsError:    result.IsError,
				}
				continue
			}
		}
		// Fallback: run via hiroto subprocess.
		out, isErr := runViaHiroto(ctx, tc.Function.Name, tc.Function.Arguments)
		results[i] = ToolResult{
			ToolCallID: tc.ID,
			Name:       tc.Function.Name,
			Content:    out,
			IsError:    isErr,
		}
	}
	return results
}
// runViaHiroto runs a tool via the `hiroto tool <name>` subprocess.
// Returns (output, is_error).
func runViaHiroto(ctx context.Context, name string, argsJSON string) (string, bool) {
	// Skip task-related tools to avoid recursion.
	if name == "task" || name == "task_create" || name == "task_wait" || name == "task_result" || name == "task_output" || name == "task_status" || name == "task_list" || name == "task_cancel" || name == "task_send" {
		return "", false
	}
	cmd := exec.CommandContext(ctx, "hiroto", "tool", name)
	cmd.Stdin = strings.NewReader(argsJSON)
	cmd.Env = append(cmd.Env, "HIROTO_SESSION="+os.Getenv("HIROTO_SESSION"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), true
	}
	// Strip trailing newline from JSON output.
	s := strings.TrimSpace(string(out))
	return s, false
}
// defaultToolDefs returns a minimal tool definition for known tools when no
// registry is available. Used by one-shot and subprocess agent invocations.
func defaultToolDefs(tools []string) []llm.ToolDefinition {
	defs := make([]llm.ToolDefinition, 0, len(tools))
	for _, name := range tools {
		switch name {
		case "terminal":
			defs = append(defs, llm.ToolDefinition{
				Name:        "terminal",
				Description: "Run a shell command",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{"type": "string", "description": "The command to run"},
					},
					"required": []any{"command"},
				},
			})
		case "read_file":
			defs = append(defs, llm.ToolDefinition{
				Name:        "read_file",
				Description: "Read file content",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "File path to read"},
					},
					"required": []any{"path"},
				},
			})
		case "write_file":
			defs = append(defs, llm.ToolDefinition{
				Name:        "write_file",
				Description: "Write content to a file",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":  map[string]any{"type": "string", "description": "File path"},
						"content": map[string]any{"type": "string", "description": "File content"},
					},
					"required": []any{"path", "content"},
				},
			})
		default:
			defs = append(defs, llm.ToolDefinition{
				Name:        name,
				Description: fmt.Sprintf("Tool %s", name),
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{},
					"required": []any{},
				},
			})
		}
	}
	return defs
}
// wrapLLMTools converts []llm.ToolDefinition into []llm.Tool.
func wrapLLMTools(defs []llm.ToolDefinition) []llm.Tool {
	out := make([]llm.Tool, len(defs))
	for i, d := range defs {
		out[i] = llm.Tool{
			Type:     "function",
			Function: d,
		}
	}
	return out
}
