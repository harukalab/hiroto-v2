package tools

import (
	"context"
	"fmt"
)

// registerAdvisor registers the `advisor` tool in the registry.
func registerAdvisor(r *Registry, rt *AdvisorRuntime) {
	r.Register(&Tool{
		Name:        "advisor",
		Description: "Control the advisor model — Hiroto's second model that watches the primary agent's transcript and provides advisory notes. Supports actions: toggle (enable/disable), status (show state), review (trigger manual review), configure (set model/tools/watchdog).",
		Parameters: mustJSON(`{
			"type": "object",
			"properties": {
				"action": {
					"type": "string",
					"enum": ["toggle", "status", "review", "configure"],
					"description": "The advisor action to perform"
				},
				"enabled": {
					"type": "boolean",
					"description": "Enable or disable the advisor (used with 'toggle')"
				},
				"model": {
					"type": "string",
					"description": "LLM model name for the advisor (used with 'configure')"
				},
				"tools": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Tool names the advisor can use (used with 'configure')"
				},
				"watchdog": {
					"type": "string",
					"description": "Watchdog mode string (used with 'configure')"
				}
			},
			"required": ["action"]
		}`),
		Exec: func(ctx context.Context, args map[string]any) Result {
			return advisorExec(ctx, args, rt)
		},
	})
}

// advisorExec dispatches advisor tool actions.
func advisorExec(ctx context.Context, args map[string]any, rt *AdvisorRuntime) Result {
	action, _ := args["action"].(string)

	switch action {
	case "toggle":
		return advisorToggle(args, rt)
	case "status":
		return advisorStatus(rt)
	case "review":
		return advisorReview(ctx, args, rt)
	case "configure":
		return advisorConfigure(args, rt)
	default:
		return Result{
			Output:  fmt.Sprintf("unknown advisor action: %q. Supported: toggle, status, review, configure", action),
			IsError: true,
		}
	}
}

// advisorToggle enables or disables the advisor.
func advisorToggle(args map[string]any, rt *AdvisorRuntime) Result {
	enabled, _ := args["enabled"].(bool)
	rt.Config.Enabled = enabled

	if enabled {
		if err := rt.Start(); err != nil {
			return Result{
				Output:  fmt.Sprintf("advisor enabled but failed to start: %s", err),
				IsError: true,
			}
		}
		return Result{Output: fmt.Sprintf("advisor enabled (model: %q)", rt.Config.Model)}
	}

	rt.Stop()
	return Result{Output: "advisor disabled"}
}

// advisorStatus returns the advisor's current state.
func advisorStatus(rt *AdvisorRuntime) Result {
	st := rt.Status()
	sb := fmt.Sprintf("enabled: %v\nmodel: %s\nimmunity_active: %v\nimmunity_turns: %d\ntranscript_size: %d\n",
		st["enabled"], st["model"], st["immunity_active"], st["immune_turns"], st["transcript_size"])
	return Result{Output: sb}
}

// advisorReview manually triggers a review of the latest transcript delta.
func advisorReview(ctx context.Context, args map[string]any, rt *AdvisorRuntime) Result {
	delta, _ := args["delta"].(string)
	if delta == "" {
		// If no delta provided, review the full transcript.
		delta = "<full transcript review>"
		for _, entry := range rt.Transcript {
			delta += "\n" + entry
		}
	}

	advisory, err := rt.Review(delta)
	if err != nil {
		return Result{
			Output:  fmt.Sprintf("advisor review failed: %s", err),
			IsError: true,
		}
	}
	if advisory == nil {
		return Result{Output: "advisor: nothing noteworthy in the transcript"}
	}

	// Emit the advisory into the transcript.
	if emitErr := rt.Emit(advisory); emitErr != nil {
		return Result{
			Output:  fmt.Sprintf("advisor: review OK but emit failed: %s", emitErr),
			IsError: true,
		}
	}

	return Result{Output: fmt.Sprintf("advisor advisory [%s]: %s\nGuidance: %s",
		advisory.Severity, advisory.Text, advisory.Guidance)}
}

// advisorConfigure updates the advisor configuration.
func advisorConfigure(args map[string]any, rt *AdvisorRuntime) Result {
	if v, ok := args["model"].(string); ok && v != "" {
		rt.Config.Model = v
	}
	if v, ok := args["tools"].([]any); ok {
		rt.Config.Tools = make([]string, len(v))
		for i, t := range v {
			rt.Config.Tools[i] = fmt.Sprintf("%v", t)
		}
	}
	if v, ok := args["watchdog"].(string); ok && v != "" {
		rt.Config.Watchdog = v
	}
	return Result{Output: fmt.Sprintf("advisor configured: model=%q, tools=%v, watchdog=%q",
		rt.Config.Model, rt.Config.Tools, rt.Config.Watchdog)}
}
