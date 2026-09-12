package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/hirotomasato/hiroto/internal/session"
)

// registerMemoryConsolidation wires up the "memory" tool with consolidation actions.
// It registers the memory tool that dispatches extract/consolidate/enqueue/clear/get/inject.
func registerMemoryConsolidation(r *Registry, pipeline *MemoryPipeline) {
	r.Register(&Tool{
		Name:        "memory",
		Description: "Memory consolidation pipeline: extract per-session memories, consolidate cross-session knowledge, and inject into future prompts. Actions: extract / consolidate / enqueue / clear / get / inject.",
		Parameters: obj(map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"extract", "consolidate", "enqueue", "clear", "get", "inject"},
				"description": "Operation to perform",
			},
			"session_id": str("Session ID to extract from (required for extract, optional for enqueue)"),
		}, "action"),
		Exec: func(ctx context.Context, args map[string]any) Result {
			return memoryExec(ctx, args, pipeline)
		},
	})
}

// memoryExec dispatches memory actions to the pipeline.
func memoryExec(ctx context.Context, args map[string]any, pipeline *MemoryPipeline) Result {
	action, _ := args["action"].(string)
	switch action {
	case "extract":
		sessionID, _ := args["session_id"].(string)
		if sessionID == "" {
			return Result{Output: "missing session_id", IsError: true}
		}
		return memoryExecExtract(sessionID, pipeline)
	case "consolidate":
		return memoryExecConsolidate(pipeline)
	case "enqueue":
		sessionID, _ := args["session_id"].(string)
		return memoryExecEnqueue(sessionID, pipeline)
	case "clear":
		return memoryExecClear(pipeline)
	case "get":
		return memoryExecGet(pipeline)
	case "inject":
		return memoryExecInject(pipeline)
	default:
		return Result{Output: fmt.Sprintf("unknown action: %s", action), IsError: true}
	}
}

func memoryExecExtract(sessionID string, pipeline *MemoryPipeline) Result {
	// Load the session.
	store := session.New()
	sess, err := store.Load(sessionID)
	if err != nil {
		return Result{Output: fmt.Sprintf("cannot load session %s: %v", sessionID, err), IsError: true}
	}
	if err := pipeline.Extract(sess); err != nil {
		return Result{Output: fmt.Sprintf("extraction failed: %v", err), IsError: true}
	}
	return Result{Output: fmt.Sprintf("extracted session %s (total entries: %d)", sessionID, pipeline.entryCount())}
}

func memoryExecConsolidate(pipeline *MemoryPipeline) Result {
	if err := pipeline.Consolidate(); err != nil {
		return Result{Output: fmt.Sprintf("consolidation failed: %v", err), IsError: true}
	}
	summary := pipeline.GetConsolidated()
	if summary == "" {
		return Result{Output: "consolidation produced empty output"}
	}
	return Result{Output: summary}
}

func memoryExecEnqueue(sessionID string, pipeline *MemoryPipeline) Result {
	if sessionID != "" {
		pipeline.Enqueue(sessionID)
		return Result{Output: fmt.Sprintf("queued session %s for extraction", sessionID)}
	}
	ids := pipeline.queuedIDs()
	return Result{Output: fmt.Sprintf("queued sessions: %s", strings.Join(ids, ", "))}
}

func memoryExecClear(pipeline *MemoryPipeline) Result {
	pipeline.Clear()
	return Result{Output: "cleared all memory entries and consolidated output"}
}

func memoryExecGet(pipeline *MemoryPipeline) Result {
	text := pipeline.GetConsolidated()
	if text == "" {
		return Result{Output: "(empty — no consolidated memory yet)"}
	}
	return Result{Output: text}
}

func memoryExecInject(pipeline *MemoryPipeline) Result {
	text := pipeline.InjectIntoPrompt()
	if text == "" {
		return Result{Output: "(empty — no consolidated memory for injection)"}
	}
	return Result{Output: text}
}
