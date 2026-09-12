package tools

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// computeLineHash returns a 2-char hex hash derived from the content of a
// file or code line.  It takes the first 2 bytes of SHA256(content) and
// formats them as lowercase hex — exactly the OMP convention.
func computeLineHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%02x%02x", h[0], h[1])
}

// computeFullFileHash returns a 6-char hex prefix of the full SHA256
// digest of the given content string, used as a short but collision-resistant
// identifier for the entire file.
func computeFullFileHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%02x%02x%02x", h[0], h[1], h[2])
}

// validateEditHash checks that every edit's OriginalHash matches the
// 2-char line-hash of the corresponding region in the current file content.
// Returns true when all edits are consistent, false otherwise.
func validateEditHash(currentContent string, edits []Edit) bool {
	lines := strings.Split(currentContent, "\n")

	for _, e := range edits {
		if e.OriginalHash == "" {
			// No hash guard — skip validation.
			continue
		}

		// Gather the lines in the original range.
		start := e.StartLine
		end := e.EndLine
		if start < 0 {
			start = 0
		}
		if end < start || end > len(lines) {
			// Range out of bounds — the hash can't match.
			return false
		}

		// Build the content that was hashed.
		var parts []string
		for i := start; i < end; i++ {
			parts = append(parts, lines[i])
		}
		regionContent := strings.Join(parts, "\n")
		lineHash := computeLineHash(regionContent)

		if lineHash != e.OriginalHash {
			return false
		}
	}

	return true
}
