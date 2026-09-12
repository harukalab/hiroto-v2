package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"




)
// DefaultEmissionsPerMinute is the default rate limit for advisory emissions.
const DefaultEmissionsPerMinute = 5

// DefaultImmuneTurns is the default cooldown turns after delivering concern/blocker.
const DefaultImmuneTurns = 3

// NewAdvisorRuntime creates a new advisor runtime with the given configuration.
func NewAdvisorRuntime(config AdvisorConfig, llmClient LLMClient, toolSession *ToolSession) *AdvisorRuntime {
	return &AdvisorRuntime{
		Config:      config,
		Client:      llmClient,
		ToolSession: toolSession,
		Transcript:  make([]string, 0),
		Watchdog:    []byte{},
		guard: EmissionGuard{
			lastSeen:  make(map[string]bool),
			recent:    make([]time.Time, 0),
			maxPerMin: DefaultEmissionsPerMinute,
			cooldown:  30 * time.Second,
		},
		immuneTurns: DefaultImmuneTurns,
	}
}

// Start initializes the advisor monitoring loop.
// Returns nil if the advisor is enabled; otherwise an error describing why it's disabled.
func (rt *AdvisorRuntime) Start() error {
	if !rt.Config.Enabled {
		return fmt.Errorf("advisor disabled")
	}
	if rt.Client == nil {
		return fmt.Errorf("advisor: no LLM client configured")
	}
	return nil
}

// Stop halts the advisor monitoring loop.
func (rt *AdvisorRuntime) Stop() {
	rt.Watchdog = []byte("stopped")
}

// Update receives a new transcript delta from the primary session.
// It appends the delta to the advisor's local transcript copy.
func (rt *AdvisorRuntime) Update(delta string) {
	rt.Transcript = append(rt.Transcript, delta)
}

// Review reviews the latest transcript delta and returns an advisory note if warranted.
func (rt *AdvisorRuntime) Review(delta string) (*Advisory, error) {
	if !rt.Config.Enabled {
		return nil, fmt.Errorf("advisor not enabled")
	}

	// Build a prompt from the latest transcript portion for review.
	prompt := rt.buildReviewPrompt(delta)

	// Send to LLM for analysis.
	response, err := rt.Client.Chat(context.Background(), []LLMMessage{
		{Role: "system", Content: "You are Hiroto's advisor model, watching the primary agent's transcript. " +
			"Analyze the provided text for issues, improvements, and concerns. " +
			"Return a concise advisory in the following format:\n" +
			"SEVERITY: nit|concern|blocker\n" +
			"TEXT: <brief description>\n" +
			"GUIDANCE: <suggested action>\n" +
			"Return nothing if there is nothing noteworthy."},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("advisor review failed: %w", err)
	}

	// Parse the response.
	advisory, err := parseAdvisoryResponse(response)
	if err != nil || advisory == nil {
		return nil, nil // nothing noteworthy
	}

	// Run through emission guard.
	if !rt.guard.Allow(advisory) {
		return nil, nil // suppressed by rate limit or dedupe
	}

	// Record emission.
	advisory.ID = generateAdvisoryID(advisory)
	rt.guard.Record(advisory)

	// Start immunity period for concern/blocker.
	if advisory.Severity == SeverityConcern || advisory.Severity == SeverityBlocker {
		rt.immuneTurns = DefaultImmuneTurns
		rt.immuneSince = time.Now().Unix()
	}

	return advisory, nil
}

// Emit injects an advisory into the primary transcript as an XML-style tag.
func (rt *AdvisorRuntime) Emit(advisory *Advisory) error {
	if advisory == nil {
		return fmt.Errorf("nil advisory")
	}

	// Ensure advisor name defaults.
	if advisory.Advisor == "" {
		advisory.Advisor = "Default"
	}

	tag := fmt.Sprintf(`<advisory advisor="%s" severity="%s">%s</advisory>`,
		advisory.Advisor, advisory.Severity, advisory.Text)

	// Append to transcript.
	rt.Transcript = append(rt.Transcript, tag)
	return nil
}

// Reset clears the advisor's transcript and resets immunity.
func (rt *AdvisorRuntime) Reset() {
	rt.Transcript = make([]string, 0)
	rt.guard = EmissionGuard{
		lastSeen:  make(map[string]bool),
		recent:    make([]time.Time, 0),
		maxPerMin: DefaultEmissionsPerMinute,
		cooldown:  30 * time.Second,
	}
	rt.immuneTurns = DefaultImmuneTurns
}

// DecrementImmuneTurns reduces the immunity counter by one turn.
// Returns the remaining immunity count.
func (rt *AdvisorRuntime) DecrementImmuneTurns() int {
	if rt.immuneTurns > 0 {
		rt.immuneTurns--
	}
	return rt.immuneTurns
}

// IsImmune returns true if the advisor is currently in an immune period
// where concern/blocker advisories are suppressed.
func (rt *AdvisorRuntime) IsImmune() bool {
	return rt.immuneTurns > 0
}

// SetImmuneTurns sets the immunity counter for the advisor runtime.
func (rt *AdvisorRuntime) SetImmuneTurns(n int) {
	rt.immuneTurns = n
}

// Status returns a map of the advisor's current state for inspection.
func (rt *AdvisorRuntime) Status() map[string]any {
	return map[string]any{
		"enabled":         rt.Config.Enabled,
		"model":           rt.Config.Model,
		"tools":           rt.Config.Tools,
		"watchdog":        string(rt.Watchdog),
		"transcript_size": len(rt.Transcript),
		"immune_turns":    rt.immuneTurns,
		"immunity_active": rt.immuneTurns > 0,
	}
}

// buildReviewPrompt assembles the prompt text for the advisor's LLM review.
func (rt *AdvisorRuntime) buildReviewPrompt(delta string) string {
	var b strings.Builder
	b.WriteString("Latest transcript delta:\n")
	b.WriteString(delta)
	b.WriteString("\n\n")

	// Include recent context (last N entries).
	window := 20
	start := 0
	if len(rt.Transcript) > window {
		start = len(rt.Transcript) - window
	}
	if start < len(rt.Transcript) {
		b.WriteString("Recent context (last " + fmt.Sprintf("%d", len(rt.Transcript)-start) + " entries):\n")
		for _, entry := range rt.Transcript[start:] {
			b.WriteString(entry + "\n")
		}
	}
	return b.String()
}

// parseAdvisoryResponse parses an LLM response into an Advisory struct.
func parseAdvisoryResponse(response string) (*Advisory, error) {
	response = strings.TrimSpace(response)
	if response == "" {
		return nil, ErrNoAdvisory
	}

	// Parse SEVERITY line.
	severities := map[string]bool{
		SeverityNit:     true,
		SeverityConcern: true,
		SeverityBlocker: true,
	}

	lines := strings.Split(response, "\n")
	var severity, text, guidance string
	inText := false
	inGuidance := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "SEVERITY:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "SEVERITY:"))
			if severities[val] {
				severity = val
			}
			inText = false
			inGuidance = false
		} else if strings.HasPrefix(trimmed, "TEXT:") {
			text = strings.TrimSpace(strings.TrimPrefix(trimmed, "TEXT:"))
			inText = true
			inGuidance = false
		} else if strings.HasPrefix(trimmed, "GUIDANCE:") {
			guidance = strings.TrimSpace(strings.TrimPrefix(trimmed, "GUIDANCE:"))
			inGuidance = true
			inText = false
		} else if inText && text != "" {
			text += " " + strings.TrimSpace(line)
		} else if inGuidance {
			guidance += " " + strings.TrimSpace(line)
		}
	}

	if severity == "" || text == "" {
		return nil, ErrNoAdvisory
	}

	return &Advisory{
		Advisor:  "Default",
		Text:     text,
		Severity: severity,
		Guidance: guidance,
	}, nil
}

// generateAdvisoryID creates a deterministic advisory ID from its content.
func generateAdvisoryID(a *Advisory) string {
	h := sha256.Sum256([]byte(a.Advisor + a.Text + a.Severity))
	return fmt.Sprintf("adv-%x", h[:8])
}

// EmissionGuard implements the guard methods needed by AdvisorRuntime.
// Allow checks if an advisory should be emitted (rate limit + dedupe).
func (g *EmissionGuard) Allow(a *Advisory) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Dedupe: check if we've seen this advisory content.
	hash := sha256.Sum256([]byte(a.Text))
	hex := fmt.Sprintf("%x", hash[:8])
	if g.lastSeen[hex] {
		return false
	}

	// Rate limit: check emissions per minute.
	now := time.Now()
	windowStart := now.Add(-time.Minute)
	recent := make([]time.Time, 0, len(g.recent))
	for _, t := range g.recent {
		if t.After(windowStart) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= g.maxPerMin {
		return false
	}

	return true
}

// Record marks an advisory as emitted in the guard state.
func (g *EmissionGuard) Record(a *Advisory) {
	g.mu.Lock()
	defer g.mu.Unlock()

	hash := sha256.Sum256([]byte(a.Text))
	hex := fmt.Sprintf("%x", hash[:8])
	g.lastSeen[hex] = true
	g.recent = append(g.recent, time.Now())

	// Prune old entries (keep last 2 minutes).
	windowStart := time.Now().Add(-2 * time.Minute)
	recent := make([]time.Time, 0)
	for _, t := range g.recent {
		if t.After(windowStart) {
			recent = append(recent, t)
		}
	}
	g.recent = recent
}
