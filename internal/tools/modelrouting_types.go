package tools

import "sync"

// ModelRole classifies the purpose of an LLM call, enabling role-based model routing.
type ModelRole string

const (
	RoleDefault ModelRole = "default" // standard chat calls
	RoleVision  ModelRole = "vision"  // vision analysis
	RoleSmol    ModelRole = "smol"    // fast, lightweight calls
	RolePlan    ModelRole = "plan"    // planning / reasoning mode
	RoleSlow    ModelRole = "slow"    // compaction and heavy reasoning
)

// ModelRoute maps a role to the model, provider, and tier level.
type ModelRoute struct {
	Role     ModelRole `json:"role"`
	Model    string    `json:"model"`
	Provider string    `json:"provider"`
	Level    string    `json:"level"` // "fast", "standard", "thinking"
}

// ProviderConfig holds connection details for a model provider.
type ProviderConfig struct {
	Name    string `json:"name"`
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseURL"`
}

// ModelRouter dispatches LLM calls to role-appropriate models.
type ModelRouter struct {
	mu          sync.RWMutex
	Routes      map[ModelRole]ModelRoute
	DefaultRole ModelRole
	Providers   map[string]ProviderConfig
}

// RouterOptions configures the ModelRouter.
type RouterOptions struct {
	// DefaultProvider is the provider name to use for all routes.
	DefaultProvider string
	// ProviderAPIKey is the API key for the default provider.
	ProviderAPIKey string
	// ProviderBaseURL is the base URL for the default provider.
	ProviderBaseURL string
}
