package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hirotomasato/hiroto/internal/llm"
)

// NewModelRouter creates a ModelRouter with sensible defaults.
func NewModelRouter() *ModelRouter {
	return NewModelRouterWithOptions(RouterOptions{
		DefaultProvider: "default",
		ProviderBaseURL: "http://localhost:11434",
	})
}

// NewModelRouterWithOptions creates a ModelRouter with custom provider configuration.
func NewModelRouterWithOptions(opts RouterOptions) *ModelRouter {
	return &ModelRouter{
		Routes: map[ModelRole]ModelRoute{
			RoleDefault: {
				Role:     RoleDefault,
				Model:    "default-model",
				Provider: opts.DefaultProvider,
				Level:    "standard",
			},
			RoleVision: {
				Role:     RoleVision,
				Model:    "vision-model",
				Provider: opts.DefaultProvider,
				Level:    "standard",
			},
			RoleSmol: {
				Role:     RoleSmol,
				Model:    "smol-model",
				Provider: opts.DefaultProvider,
				Level:    "fast",
			},
			RolePlan: {
				Role:     RolePlan,
				Model:    "plan-model",
				Provider: opts.DefaultProvider,
				Level:    "thinking",
			},
			RoleSlow: {
				Role:     RoleSlow,
				Model:    "slow-model",
				Provider: opts.DefaultProvider,
				Level:    "thinking",
			},
		},
		DefaultRole: RoleDefault,
		Providers: map[string]ProviderConfig{
			opts.DefaultProvider: {
				Name:    opts.DefaultProvider,
				APIKey:  opts.ProviderAPIKey,
				BaseURL: opts.ProviderBaseURL,
			},
		},
	}
}
// SetRoute sets the model for a given role. Returns an error for invalid roles.
func (r *ModelRouter) SetRoute(role ModelRole, route ModelRoute) error {
	if _, ok := allRoles()[role]; !ok {
		return fmt.Errorf("unknown role: %q", role)
	}
	route.Role = role
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Routes[role] = route
	return nil
}

// GetRoute returns the model route for a role.
func (r *ModelRouter) GetRoute(role ModelRole) (ModelRoute, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	route, ok := r.Routes[role]
	if !ok {
		return ModelRoute{}, fmt.Errorf("no route for role: %q", role)
	}
	return route, nil
}

// Resolve returns an LLM client configured for the given role, with caching.
func (r *ModelRouter) Resolve(role ModelRole) (ModelLLM, error) {
	r.mu.RLock()
	route, ok := r.Routes[role]
	providers := make(map[string]ProviderConfig, len(r.Providers))
	for k, v := range r.Providers {
		providers[k] = v
	}
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no route for role: %q", role)
	}

	pcfg, ok := providers[route.Provider]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", route.Provider)
	}

	return &ClientCache{
		BaseURL: pcfg.BaseURL,
		APIKey:  pcfg.APIKey,
		Model:   route.Model,
	}, nil
}

// SetProvider registers provider configuration.
func (r *ModelRouter) SetProvider(name, apiKey, baseURL string) error {
	if name == "" {
		return fmt.Errorf("provider name required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Providers[name] = ProviderConfig{
		Name:    name,
		APIKey:  apiKey,
		BaseURL: baseURL,
	}
	return nil
}

// ListRoutes returns a snapshot of all routes.
func (r *ModelRouter) ListRoutes() map[ModelRole]ModelRoute {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[ModelRole]ModelRoute, len(r.Routes))
	for k, v := range r.Routes {
		out[k] = v
	}
	return out
}

// NormalizeModelID returns a provider-prefixed canonical model ID.
// Recognized formats:
//   - "provider/model" → already prefixed
//   - "model" → prefixed with the default provider
//   - "provider::model" → legacy double-colon format
func (r *ModelRouter) NormalizeModelID(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	// Already provider-prefixed (provider/model)
	if strings.Contains(model, "/") && !strings.HasPrefix(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		provider := parts[0]
		// Check if provider exists in our registry
		r.mu.RLock()
		_, ok := r.Providers[provider]
		r.mu.RUnlock()
		if ok {
			return model
		}
	}
	// Legacy double-colon format
	if idx := strings.Index(model, "::"); idx >= 0 {
		return model[idx+2:]
	}
	// Unprefixed: use the default provider
	r.mu.RLock()
	defaultRoute, _ := r.Routes[r.DefaultRole]
	provider := defaultRoute.Provider
	r.mu.RUnlock()
	if provider == "" {
		return model
	}
	return provider + "/" + model
}

// SelectBest matches a model name to the best-fitting route.
func (r *ModelRouter) SelectBest(model string) ModelRoute {
	lower := strings.ToLower(model)

	r.mu.RLock()
	defer r.mu.RUnlock()

	// Check each route for a match
	for _, route := range r.Routes {
		routeModel := strings.ToLower(route.Model)
		if routeModel == lower || routeModel == "default-model" {
			// For "default-model", pick the best match by name
			if strings.Contains(lower, routeModel) || strings.Contains(routeModel, lower) {
				return route
			}
		}
		if routeModel != "" && (strings.Contains(lower, routeModel) || strings.Contains(routeModel, lower)) {
			return route
		}
	}

	// Fallback: heuristic based on keywords in the model name
	switch {
	case strings.Contains(lower, "vision"), strings.Contains(lower, "clip"), strings.Contains(lower, "llava"), strings.Contains(lower, "grok"):
		return r.Routes[RoleVision]
	case strings.Contains(lower, "smol"), strings.Contains(lower, "small"), strings.Contains(lower, "turbo"), strings.Contains(lower, "fast"):
		return r.Routes[RoleSmol]
	case strings.Contains(lower, "reason"), strings.Contains(lower, "thinking"), strings.Contains(lower, "o3"), strings.Contains(lower, "o1"), strings.Contains(lower, "sonnet"):
		return r.Routes[RolePlan]
	case strings.Contains(lower, "large"), strings.Contains(lower, "max"), strings.Contains(lower, "pro"), strings.Contains(lower, "ultra"):
		return r.Routes[RoleSlow]
	}

	// Default fallback
	return r.Routes[r.DefaultRole]
}

// ModelLLM is the interface for role-based LLM clients.
// Chat is non-streaming; Stream supports SSE streaming with a callback.
type ModelLLM interface {
	Chat(ctx context.Context, messages []LLMMessage) (string, error)
	Stream(ctx context.Context, messages []LLMMessage, tools []LLMTool, onText func(string)) (string, error)
}

// ClientCache wraps an LLM client with a simple cache to avoid recreating
// llm.Client instances for the same configuration.
type ClientCache struct {
	BaseURL string
	APIKey  string
	Model   string
	mu      sync.Mutex
	client  *llm.Client
}

func (c *ClientCache) Chat(ctx context.Context, messages []LLMMessage) (string, error) {
	c.mu.Lock()
	if c.client == nil {
		c.client = llm.New(c.BaseURL, c.APIKey, c.Model)
	}
	client := c.client
	c.mu.Unlock()

	llmMsgs := make([]llm.Message, len(messages))
	for i, m := range messages {
		llmMsgs[i] = llm.Message{
			Role:    llm.Role(m.Role),
			Content: m.Content,
		}
	}

	resp, err := client.Chat(ctx, llmMsgs, nil)
	if err != nil {
		return "", err
	}
	if s, ok := resp.Content.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", resp.Content), nil
}
// Stream performs an SSE-streaming completion on the routed model.
func (c *ClientCache) Stream(ctx context.Context, messages []LLMMessage, tools []LLMTool, onText func(string)) (string, error) {
	c.mu.Lock()
	if c.client == nil {
		c.client = llm.New(c.BaseURL, c.APIKey, c.Model)
	}
	client := c.client
	c.mu.Unlock()

	llmMsgs := make([]llm.Message, len(messages))
	for i, m := range messages {
		llmMsgs[i] = llm.Message{
			Role:    llm.Role(m.Role),
			Content: m.Content,
		}
	}

	llmTools := make([]llm.Tool, len(tools))
	for i, t := range tools {
		llmTools[i] = llm.Tool{
			Type: t.Type,
			Function: llm.ToolDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		}
	}

	resp, err := client.Stream(ctx, llmMsgs, llmTools, onText)
	if err != nil {
		return "", err
	}
	if s, ok := resp.Content.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", resp.Content), nil
}

// GetClient returns the cached *llm.Client, initializing it if necessary.
func (c *ClientCache) GetClient() *llm.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		c.client = llm.New(c.BaseURL, c.APIKey, c.Model)
	}
	return c.client
}

// allRoles returns the set of valid ModelRole values.
func allRoles() map[ModelRole]struct{} {
	return map[ModelRole]struct{}{
		RoleDefault: {},
		RoleVision:  {},
		RoleSmol:    {},
		RolePlan:    {},
		RoleSlow:    {},
	}
}
