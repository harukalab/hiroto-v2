package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// helpLine is the bottom key-hint bar.
const helpLine = "Enter send • Ctrl+P model • Ctrl+R session • Ctrl+S save • Ctrl+L clear • ↑↓ history • scroll/PgUp-Dn scroll • Ctrl+C exit"

// layoutParts builds the frame sections. includeVP controls whether the
// transcript viewport is part of the result:
//
//   - View() calls it with true  → the full frame to render.
//   - recalcViewport() calls it with false → the CHROME only (everything that
//     shares the terminal with the viewport). Including the viewport in the
//     measurement would be circular: the viewport's height is exactly what we
//     are solving for.
//
// This is the single source of truth for layout AND chrome measurement, so
// View() and recalcViewport() can never drift apart again.
//
// Empty sections are SKIPPED, not emitted: lipgloss.JoinVertical still emits
// one row per empty string, so an absent clarify/flash bar or an empty todo
// panel would otherwise add phantom rows and make the frame taller than the
// terminal — which scrolled the banner's top rule off-screen.
func (m *model) layoutParts(includeVP bool) []string {
	parts := make([]string, 0, 8)

	if includeVP {
		if v := m.vp.View(); v != "" {
			parts = append(parts, v)
		}
	}
	if v := m.renderTodoPanel(); v != "" {
		parts = append(parts, v)
	}
	if m.clarifyQuestion != "" {
		parts = append(parts, stBanner.Render("◆ "+m.clarifyQuestion))
	}
	if m.flash != "" {
		if m.flashKind == "error" {
			parts = append(parts, stFlashErr.Render(m.flash))
		} else {
			parts = append(parts, stFlashInfo.Render(m.flash))
		}
	}
	parts = append(parts, stInput.Render(m.input.View()))
	parts = append(parts, m.statusBarText())
	parts = append(parts, stHelp.Render(helpLine))
	return parts
}

// statusBarText renders the bottom status line: HR chip, version, token
// estimate, and the busy/"siap" indicator.
func (m *model) statusBarText() string {
	status := stMuted.Render("ready")
	if m.busy {
		label := "working"
		if m.streaming {
			label = "writing"
		}
		status = stToolTag.Render(m.spinner.View() + " " + label)
	}
	if len(m.ag.Messages) > 0 {
		tok := 0
		for _, msg := range m.ag.Messages {
			if s, ok := msg.Content.(string); ok {
				tok += len(s) / 3
			}
		}
		status = stMuted.Render(fmt.Sprintf("~%dK", tok/1000)) + "  " + status
	}
	left := stChip.Render(" HR ") + stMuted.Render(" hiroto v"+version)
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(status) - 2
	if pad < 1 {
		pad = 1
	}
	return left + strings_Repeat(pad) + status
}

func strings_Repeat(n int) string {
	if n < 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
