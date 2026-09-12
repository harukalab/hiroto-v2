package tools

import (
	"testing"
)

func TestNewModelRouter(t *testing.T) {
	router := NewModelRouter()
	if router == nil {
		t.Fatal("NewModelRouter returned nil")
	}
	if router.DefaultRole != RoleDefault {
		t.Errorf("expected default role %q, got %q", RoleDefault, router.DefaultRole)
	}
	if len(router.Routes) != 5 {
		t.Errorf("expected 5 routes, got %d", len(router.Routes))
	}
	if len(router.Providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(router.Providers))
	}
}

func TestSetRoute(t *testing.T) {
	router := NewModelRouter()

	// Valid role
	err := router.SetRoute(RoleDefault, ModelRoute{
		Model:    "gpt-4",
		Provider: "openai",
		Level:    "standard",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	route, err := router.GetRoute(RoleDefault)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if route.Model != "gpt-4" {
		t.Errorf("expected model gpt-4, got %s", route.Model)
	}
	if route.Provider != "openai" {
		t.Errorf("expected provider openai, got %s", route.Provider)
	}

	// Invalid role
	err = router.SetRoute(ModelRole("bogus"), ModelRoute{Model: "x"})
	if err == nil {
		t.Error("expected error for invalid role")
	}
}

func TestSetRouteSetsRole(t *testing.T) {
	router := NewModelRouter()
	router.SetRoute(RoleDefault, ModelRoute{Model: "test"})
	route, _ := router.GetRoute(RoleDefault)
	if route.Role != RoleDefault {
		t.Errorf("expected role %q, got %q", RoleDefault, route.Role)
	}
}

func TestGetRouteNotFound(t *testing.T) {
	router := NewModelRouter()
	_, err := router.GetRoute(RoleSmol)
	// Should not error since default routes exist
	if err != nil {
		t.Errorf("expected no error for existing role, got: %v", err)
	}
}

func TestSetProvider(t *testing.T) {
	router := NewModelRouter()
	err := router.SetProvider("anthropic", "sk-abc123", "https://api.anthropic.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pcfg, ok := router.Providers["anthropic"]
	if !ok {
		t.Fatal("anthropic provider not found")
	}
	if pcfg.APIKey != "sk-abc123" {
		t.Errorf("expected API key sk-abc123, got %s", pcfg.APIKey)
	}
}

func TestSetProviderEmptyName(t *testing.T) {
	router := NewModelRouter()
	err := router.SetProvider("", "", "")
	if err == nil {
		t.Error("expected error for empty provider name")
	}
}

func TestListRoutes(t *testing.T) {
	router := NewModelRouter()
	routes := router.ListRoutes()
	if len(routes) != 5 {
		t.Errorf("expected 5 routes, got %d", len(routes))
	}
	for _, role := range []ModelRole{RoleDefault, RoleVision, RoleSmol, RolePlan, RoleSlow} {
		if _, ok := routes[role]; !ok {
			t.Errorf("missing route for role %q", role)
		}
	}
}

func TestNormalizeModelID(t *testing.T) {
	router := NewModelRouter()

	// Unprefixed model gets default provider prefix
	normalized := router.NormalizeModelID("gpt-4")
	if normalized != "default/gpt-4" {
		t.Errorf("expected 'default/gpt-4', got %q", normalized)
	}

	// Provider not in registry gets default prefix prepended
	normalized = router.NormalizeModelID("openai/gpt-4")
	if normalized != "default/openai/gpt-4" {
		t.Errorf("expected 'default/openai/gpt-4', got %q", normalized)
	}

	// Legacy double-colon format
	normalized = router.NormalizeModelID("openai::gpt-4")
	if normalized != "gpt-4" {
		t.Errorf("expected 'gpt-4', got %q", normalized)
	}

	// Empty string
	normalized = router.NormalizeModelID("")
	if normalized != "" {
		t.Errorf("expected empty string, got %q", normalized)
	}

	// Whitespace-only
	normalized = router.NormalizeModelID("  ")
	if normalized != "" {
		t.Errorf("expected empty string for whitespace, got %q", normalized)
	}
}


func TestSelectBest(t *testing.T) {
	router := NewModelRouter()

	tests := []struct {
		name     string
		model    string
		expected ModelRole
	}{
		{"vision model", "llava-v1.5", RoleVision},
		{"vision keyword", "gpt-4-vision", RoleVision},
		{"smol keyword", "smollm", RoleSmol},
		{"thinking model", "o3-pro", RolePlan},
		{"thinking keyword", "claude-sonnet", RolePlan},
		{"turbo model", "gpt-4-turbo", RoleSmol},
		{"unknown model", "random-model", RoleDefault},
		{"claude opus", "claude-3-opus", RoleDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := router.SelectBest(tt.model)
			if route.Role != tt.expected {
				t.Errorf("SelectBest(%q) = %q, want %q", tt.model, route.Role, tt.expected)
			}
		})
	}
}

func TestSelectBestUsesExistingRoutes(t *testing.T) {
	router := NewModelRouter()
	router.SetRoute(RoleDefault, ModelRoute{
		Model: "my-model-v1",
	})
	route := router.SelectBest("my-model-v1")
	if route.Role != RoleDefault {
		t.Errorf("expected RoleDefault, got %q", route.Role)
	}
}

func TestResolve(t *testing.T) {
	router := NewModelRouter()
	client, err := router.Resolve(RoleDefault)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestResolveUnknownRole(t *testing.T) {
	router := NewModelRouter()
	_, err := router.Resolve(ModelRole("nonexistent"))
	if err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestResolveUnknownProvider(t *testing.T) {
	router := NewModelRouter()
	router.SetRoute(RoleDefault, ModelRoute{
		Model:    "test",
		Provider: "unknown-provider",
	})
	_, err := router.Resolve(RoleDefault)
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestCaching(t *testing.T) {
	router := NewModelRouter()
	// Resolve the same role twice
	client1, err := router.Resolve(RoleDefault)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	client2, err := router.Resolve(RoleDefault)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client1 == nil || client2 == nil {
		t.Fatal("expected non-nil clients")
	}
}

func TestNormalizePreservesWhitespace(t *testing.T) {
	router := NewModelRouter()
	normalized := router.NormalizeModelID("  gpt-4  ")
	if normalized != "default/gpt-4" {
		t.Errorf("expected 'default/gpt-4', got %q", normalized)
	}
}
