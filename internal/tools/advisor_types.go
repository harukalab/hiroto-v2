package tools

import (
	"errors"
	"sync"
	"time"
)

// ErrNoAdvisory is returned when the advisor produces no actionable note.
var ErrNoAdvisory = errors.New("no advisory: empty or incomplete response")

// Severity levels for advisory notes.
const (
	SeverityNit     = "nit"      // non-interrupting, informational
	SeverityConcern = "concern"  // interrupting, warrants attention
	SeverityBlocker = "blocker"  // critical, requires immediate action
)

// AdvisorConfig holds the advisor model's configuration.
type AdvisorConfig struct {
	Enabled  bool
	Model    string
	Tools    []string
	Watchdog string
}

// Advisory is a note produced by the advisor about a transcript delta.
type Advisory struct {
	ID       string
	Advisor  string
	Text     string
	Severity string
	Guidance string
}

// EmissionGuard controls advisory emission: rate limiting, dedupe, and normalization.

// ToolSession represents the primary session context that the advisor can
// observe (metadata, active tool, etc.). The advisor does not modify it.
type ToolSession struct {
	Name    string
	Tool    string // currently active tool name, if any
}
type EmissionGuard struct {
	mu         sync.Mutex
	lastSeen   map[string]bool   // hash → last-emitted
	recent     []time.Time       // timestamps of recent emissions
	maxPerMin  int               // max emissions per minute
	cooldown   time.Duration     // per-advisor-name cooldown
}

// AdvisorRuntime is the advisor model's runtime state.
type AdvisorRuntime struct {
	Config      AdvisorConfig
	Client      LLMClient
	Transcript  []string
	ToolSession *ToolSession

	// Watchdog tracks advisor monitoring state.
	Watchdog []byte

	// EmissionGuard controls advisory emission rate.
	guard EmissionGuard

	// ImmuneTurns counts turns remaining where concern/blocker advisories
	// from this advisor are suppressed (after delivering one).
	immuneTurns int
	immuneSince int64 // turn count when last immunity started
}
