package tools

import (
	"context"
	"fmt"
	"strings"
)

// registerStreamRules registers the stream_rules tool with the registry.
func registerStreamRules(r *Registry, engine *RuleEngine) {
	r.Register(&Tool{
		Name:        "stream_rules",
		Description: "Manage and apply regex-based rules that inject text when matching tokens appear in LLM output. Supports add, remove, list, match, and apply actions.",
		Parameters: mustJSON(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "description": "Action: add, remove, list, match, apply", "enum": ["add", "remove", "list", "match", "apply"]},
				"pattern": {"type": "string", "description": "Regex pattern to match tokens (add action)"},
				"rule": {"type": "string", "description": "Rule text to inject when pattern matches (add action)"},
				"persist": {"type": "boolean", "description": "Whether the rule persists across sessions (add action)"},
				"id": {"type": "string", "description": "Rule ID to remove (remove action)"},
				"token": {"type": "string", "description": "Token to test against rules (match action)"},
				"prompt": {"type": "string", "description": "Prompt to run with rule injection (apply action)"},
				"max_retries": {"type": "integer", "description": "Max rule-injection retries (apply action, default 10)"}
			},
			"required": ["action"]
		}`),
		Exec: func(ctx context.Context, args map[string]any) Result {
			return streamRulesExec(ctx, args, engine)
		},
	})
}

// streamRulesExec dispatches stream_rules actions.
func streamRulesExec(ctx context.Context, args map[string]any, engine *RuleEngine) Result {
	action, _ := args["action"].(string)
	switch action {
	case "add":
		return addRule(args, engine)
	case "remove":
		return removeRule(args, engine)
	case "list":
		return listRules(engine)
	case "match":
		return matchToken(args, engine)
	case "apply":
		return applyRules(ctx, args, engine)
	default:
		return Result{Output: fmt.Sprintf("Unknown action: %s", action), IsError: true}
	}
}

func addRule(args map[string]any, engine *RuleEngine) Result {
	pattern, _ := args["pattern"].(string)
	rule, _ := args["rule"].(string)
	persist, _ := args["persist"].(bool)

	if pattern == "" {
		return Result{Output: "pattern is required", IsError: true}
	}
	if rule == "" {
		return Result{Output: "rule text is required", IsError: true}
	}

	id, err := engine.AddRule(StreamRule{
		Pattern: pattern,
		Rule:    rule,
		Persist: persist,
	})
	if err != nil {
		return Result{Output: fmt.Sprintf("Failed to add rule: %s", err), IsError: true}
	}
	return Result{Output: fmt.Sprintf("Rule added with ID: %s", id)}
}

func removeRule(args map[string]any, engine *RuleEngine) Result {
	id, _ := args["id"].(string)
	if id == "" {
		return Result{Output: "id is required", IsError: true}
	}

	engine.RemoveRule(id)
	return Result{Output: fmt.Sprintf("Rule %s removed", id)}
}

func listRules(engine *RuleEngine) Result {
	rules := engine.ListRules()
	if len(rules) == 0 {
		return Result{Output: "No rules configured."}
	}

	var sb strings.Builder
	sb.WriteString("Active rules:\n")
	for _, r := range rules {
		sb.WriteString(fmt.Sprintf("  ID: %s\n", r.ID))
		sb.WriteString(fmt.Sprintf("    Pattern: %s\n", r.Pattern))
		sb.WriteString(fmt.Sprintf("    Rule: %s\n", r.Rule))
		sb.WriteString(fmt.Sprintf("    Persist: %v\n", r.Persist))
	}
	return Result{Output: sb.String()}
}

func matchToken(args map[string]any, engine *RuleEngine) Result {
	token, _ := args["token"].(string)
	if token == "" {
		return Result{Output: "token is required", IsError: true}
	}

	matchedRule := engine.Match(token)
	if matchedRule == "" {
		return Result{Output: fmt.Sprintf("Token %q matched no rules", token)}
	}
	return Result{Output: fmt.Sprintf("Token %q matched rule: %s", token, matchedRule)}
}

func applyRules(ctx context.Context, args map[string]any, engine *RuleEngine) Result {
	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		return Result{Output: "prompt is required", IsError: true}
	}

	if engine.RuleLLM == nil {
		return Result{Output: "No LLM client configured on engine", IsError: true}
	}

	result, err := engine.ApplyAndRetry(ctx, engine.RuleLLM, "", prompt)
	if err != nil {
		return Result{Output: fmt.Sprintf("Apply failed: %s", err), IsError: true}
	}

	return Result{Output: result}
}
