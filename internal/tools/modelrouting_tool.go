package tools

import (
	"context"
	"fmt"
	"strings"
)

// registerModelRouting registers the `model_routing` tool in the registry.
func registerModelRouting(r *Registry, router *ModelRouter) {
	r.Register(&Tool{
		Name:        "model_routing",
		Description: "Manage role-based model routing: set/get/list routes, resolve role to LLM client, normalize model IDs.",
		Parameters: mustJSON(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "description": "Action to perform: set, get, list, resolve, normalize"},
				"role": {"type": "string", "description": "Model role: default, vision, smol, plan, slow (used with set, get, resolve)"},
				"model": {"type": "string", "description": "Model name (used with set, normalize)"},
				"provider": {"type": "string", "description": "Provider name (used with set)"},
				"level": {"type": "string", "description": "Tier: fast, standard, thinking (used with set)"}
			},
			"required": ["action"]
		}`),
		Exec: func(ctx context.Context, args map[string]any) Result {
			return modelRoutingExec(ctx, args, router)
		},
	})
}

// modelRoutingExec dispatches model_routing tool actions.
func modelRoutingExec(ctx context.Context, args map[string]any, router *ModelRouter) Result {
	action, _ := args["action"].(string)
	switch strings.ToLower(action) {
	case "set":
		return setRoute(args, router)
	case "get":
		return getRoute(args, router)
	case "list":
		return listRoutes(router)
	case "resolve":
		return resolveRole(args, router)
	case "normalize":
		return normalizeModelID(args, router)
	default:
		return Result{
			Output:  fmt.Sprintf("unknown action: %q. Supported: set, get, list, resolve, normalize", action),
			IsError: true,
		}
	}
}

func setRoute(args map[string]any, router *ModelRouter) Result {
	roleStr, _ := args["role"].(string)
	role := ModelRole(roleStr)

	route := ModelRoute{Role: role}
	if model, ok := args["model"].(string); ok {
		route.Model = model
	}
	if provider, ok := args["provider"].(string); ok {
		route.Provider = provider
	}
	if level, ok := args["level"].(string); ok {
		route.Level = level
	}

	if err := router.SetRoute(role, route); err != nil {
		return Result{Output: err.Error(), IsError: true}
	}
	return Result{
		Output: fmt.Sprintf("route set: role=%s model=%s provider=%s level=%s",
			role, route.Model, route.Provider, route.Level),
	}
}

func getRoute(args map[string]any, router *ModelRouter) Result {
	roleStr, _ := args["role"].(string)
	role := ModelRole(roleStr)

	route, err := router.GetRoute(role)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}
	}
	return Result{
		Output: fmt.Sprintf("role=%s model=%s provider=%s level=%s",
			route.Role, route.Model, route.Provider, route.Level),
	}
}

func listRoutes(router *ModelRouter) Result {
	routes := router.ListRoutes()
	var b strings.Builder
	b.WriteString("Model routes:\n\n")
	for role, route := range routes {
		fmt.Fprintf(&b, "  %s → model=%s provider=%s level=%s\n", role, route.Model, route.Provider, route.Level)
	}
	return Result{Output: b.String()}
}

func resolveRole(args map[string]any, router *ModelRouter) Result {
	roleStr, _ := args["role"].(string)
	role := ModelRole(roleStr)

	llmClient, err := router.Resolve(role)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}
	}

	route, _ := router.GetRoute(role)
	return Result{
		Output: fmt.Sprintf("resolved %s → model=%s provider=%s (LLM ready: %T)",
			role, route.Model, route.Provider, llmClient),
	}
}

func normalizeModelID(args map[string]any, router *ModelRouter) Result {
	model, _ := args["model"].(string)
	if model == "" {
		return Result{Output: "missing model", IsError: true}
	}
	normalized := router.NormalizeModelID(model)
	return Result{
		Output: fmt.Sprintf("normalize: %s → %s", model, normalized),
	}
}
