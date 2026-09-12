package main

// Banner & identity: big gradient HIROTO wordmark, decorative frame, version, credits.

import (
	"fmt"
	"strings"

	"github.com/hirotomasato/hiroto/internal/config"

	"github.com/charmbracelet/lipgloss"
)

const version = "0.6.2"
const devCredit = "masantoid"

// stChip is the small "HR" logo chip used in the status bar and compact banner.
var stChip = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#161310")).
	Background(lipgloss.Color("#E8A33D")).
	Padding(0, 1)

// Tagline / rule — warm gold so the banner matches assets/hiroto.svg.
var (
	stTagline = lipgloss.NewStyle().Foreground(lipgloss.Color("#F2C14E"))
	stRule    = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8A33D"))
)

// ANSI-shadow glyphs (6 rows each, fixed width per letter).
var bannerGlyphs = map[rune][]string{
	'H': {"██╗  ██╗", "██║  ██║", "███████║", "██╔══██║", "██║  ██║", "╚═╝  ╚═╝"},
	'I': {" ██████╗", " ╚═════╝", "    ██║ ", "    ██║ ", "    ██║ ", "    ╚═╝ "},
	'R': {"██████╗ ", "██╔══██╗", "██████╔╝", "██╔══██╗", "██║  ██║", "╚═╝  ╚═╝"},
	'O': {" ██████╗ ", "██╔═══██╗", "██║   ██║", "██║   ██║", "╚██████╔╝", " ╚═════╝ "},
	'T': {"████████╗", "╚══██╔══╝", "   ██║   ", "   ██║   ", "   ██║   ", "   ╚═╝   "},
}

// bannerRows composes a word into 6 rows of block glyphs.
func bannerRows(word string) []string {
	var rows [][]string
	for _, r := range strings.ToUpper(word) {
		if g, ok := bannerGlyphs[r]; ok {
			rows = append(rows, g)
		}
	}
	var out []string
	for y := range 6 {
		var b strings.Builder
		for x, g := range rows {
			b.WriteString(g[y])
			if x != len(rows)-1 {
				b.WriteString(" ")
			}
		}
		out = append(out, b.String())
	}
	return out
}

// gradient4 paints non-space runes with a 4-stop horizontal gradient:
// gold → orange → coral → warm crimson.
func gradient4(lines []string) []string {
	stops := []struct {
		rgb [3]int
		t   float64
	}{
		{[3]int{0xF2, 0xC1, 0x4E}, 0.0},
		{[3]int{0xE8, 0xA3, 0x3D}, 0.33},
		{[3]int{0xE0, 0x70, 0x40}, 0.66},
		{[3]int{0xC4, 0x45, 0x36}, 1.0},
	}

	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		rs := []rune(ln)
		var b strings.Builder
		for _, r := range rs {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			t := 0.0
			if len(rs) > 1 {
				t = float64(len(b.String())) / float64(len(rs)-1)
			}
			rgb := interpolate(stops, t)
			hex := fmt.Sprintf("#%02X%02X%02X", rgb[0], rgb[1], rgb[2])
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(string(r)))
		}
		out = append(out, b.String())
	}
	return out
}

// interpolate returns the RGB color at position t along the gradient defined by stops.
func interpolate(stops []struct {
	rgb [3]int
	t   float64
}, t float64) [3]int {
	if len(stops) == 1 {
		return stops[0].rgb
	}
	if t <= stops[0].t {
		return stops[0].rgb
	}
	if t >= stops[len(stops)-1].t {
		return stops[len(stops)-1].rgb
	}
	for i := range len(stops) - 1 {
		if t >= stops[i].t && t <= stops[i+1].t {
			localT := (t - stops[i].t) / (stops[i+1].t - stops[i].t)
			return blend(stops[i].rgb, stops[i+1].rgb, localT)
		}
	}
	return stops[len(stops)-1].rgb
}

// blend interpolates between two RGB colors by factor f.
func blend(a, b [3]int, f float64) [3]int {
	var rgb [3]int
	for c := range 3 {
		rgb[c] = a[c] + int(float64(b[c]-a[c])*f)
	}
	return rgb
}

// rules renders a gold horizontal rule of the given width.
func rules(width int) string {
	if width > 56 {
		width = 56
	}
	if width < 24 {
		width = 24
	}
	return stRule.Render(strings.Repeat("─", width))
}

// centerPad centers a rendered string into a fixed width via leading spaces.
func centerPad(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", (width-w)/2) + s
}

// bannerLines returns the full startup banner (big word + tagline + credits).
// Falls back to a compact one-liner on narrow terminals.
// bannerLines returns a thin identity header with big HIROTO wordmark.
func bannerLines(width int, modelInfo string, skillCount int) []string {
	rule := rules(width)
	bannerW := ruleWidth(width)

	var out []string
	out = append(out, rule)
	out = append(out, gradient4(bannerRows("HIROTO"))...)
	if width >= 48 {
		out = append(out, centerPad(stMuted.Render("model: "+modelInfo), bannerW))
		if skillCount > 0 {
			out = append(out, centerPad(stMuted.Render(fmt.Sprintf("%d skill%s loaded", skillCount, plural(skillCount))), bannerW))
		}
	}
	out = append(out, rule, "")
	return out
}
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func ruleWidth(width int) int {
	if width > 56 {
		return 56
	}
	if width < 24 {
		return 24
	}
	return width
}

// setWindowTitle names the terminal tab "HR · hiroto" (OSC 9;22 with an
// OSC 2 fallback for terminals that don't support shell-integration marks).
func setWindowTitle() {
	fmt.Print("\x1b]9;22;HR · hiroto\x1b\\") // OSC 9;22 final
	fmt.Print("\x1b]2;HR · hiroto\x07")      // classic window title
}

// oneShotHeader prints a thin one-line identity header for `-q` runs.
func oneShotHeader(cfg *config.Config, skillCount int) {
	line := stChip.Render("HR") + stBanner.Render(" HIROTO") +
		stTagline.Render(" v"+version+" · "+devCredit+" · "+cfg.Model.Name+fmt.Sprintf(" · skills: %d", skillCount))
	fmt.Println(line)
	fmt.Println(stRule.Render(strings.Repeat("─", 56)))
}
