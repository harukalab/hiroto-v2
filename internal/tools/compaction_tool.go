package tools

import (
	"context"
	"fmt"
)

// registerCompaction wires up the "compact" tool in the registry.
func registerCompaction(r *Registry, engine *CompactionEngine) {
	r.Register(&Tool{
		Name:        "compact",
		Description: "Auto context compression: compress conversation history to reduce token usage. Actions: compact, auto, shake, config, status.",
		Parameters: obj(map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"compact", "auto", "shake", "config", "status"},
				"description": "Action to perform",
			},
			"instructions": str("Optional instructions for the compaction LLM (used with compact action)"),
			"method":       str("Compaction method: shake or llm (used with config action to set)"),
			"threshold":    map[string]any{"type": "number", "description": "Token threshold for auto-compaction (used with config action)"},
		}, "action"),
		Exec: func(ctx context.Context, args map[string]any) Result {
			return compactExec(ctx, args, engine)
		},
	})
}

// compactExec dispatches compact tool actions.
func compactExec(ctx context.Context, args map[string]any, engine *CompactionEngine) Result {
	action, _ := args["action"].(string)
	switch action {
	case "compact":
		return compactExecCompact(args, engine)
	case "auto":
		return compactExecAuto(engine)
	case "shake":
		return compactExecShake(ctx, engine)
	case "config":
		return compactExecConfig(args, engine)
	case "status":
		return compactExecStatus(engine)
	default:
		return Result{
			Output:  fmt.Sprintf("unknown action: %q. Supported: compact, auto, shake, config, status", action),
			IsError: true,
		}
	}
}

func compactExecCompact(args map[string]any, engine *CompactionEngine) Result {
	instructions, _ := args["instructions"].(string)
	cfg := engine.Config
	cfg.Enabled = true
	if instructions != "" {
		return Result{Output: fmt.Sprintf("compaction configured: method=%s, threshold=%d tokens, instructions: %s", cfg.Method, cfg.ThresholdTokens, truncateStr(instructions, 200))}
	}
	return Result{Output: fmt.Sprintf("compaction ready: method=%s, threshold=%d tokens", cfg.Method, cfg.ThresholdTokens)}
}

func compactExecAuto(engine *CompactionEngine) Result {
	if engine.Config.Method == "" {
		engine.Config.Method = "shake"
	}
	if engine.Config.ThresholdTokens == 0 {
		engine.Config.ThresholdTokens = 100_000
	}
	return Result{Output: fmt.Sprintf("auto-check: compaction %s, method=%s, threshold=%d tokens",
		map[bool]string{true: "enabled", false: "disabled"}[engine.Config.Enabled],
		engine.Config.Method,
		engine.Config.ThresholdTokens)}
}

func compactExecShake(ctx context.Context, engine *CompactionEngine) Result {
	if !engine.Config.Enabled {
		engine.Config.Enabled = true
		engine.Config.Method = "shake"
	}
	_ = ctx
	return Result{Output: fmt.Sprintf("shake compression configured; threshold=%d tokens", engine.Config.ThresholdTokens)}
}

func compactExecConfig(args map[string]any, engine *CompactionEngine) Result {
	if method, ok := args["method"].(string); ok {
		engine.Config.Method = method
	}
	if threshold, ok := args["threshold"].(float64); ok {
		engine.Config.ThresholdTokens = int(threshold)
	}
	return Result{Output: fmt.Sprintf("config updated: method=%s, threshold=%d tokens, enabled=%v",
		engine.Config.Method,
		engine.Config.ThresholdTokens,
		engine.Config.Enabled)}
}

func compactExecStatus(engine *CompactionEngine) Result {
	return Result{Output: fmt.Sprintf("compaction status: enabled=%v, method=%s, threshold=%d tokens, history entries=%d",
		engine.Config.Enabled,
		engine.Config.Method,
		engine.Config.ThresholdTokens,
		len(engine.History))}
}

// truncateStr truncates s to max runes, appending "…" if truncated.
func truncateStr(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
