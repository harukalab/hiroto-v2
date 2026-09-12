package tools

// Edit represents a single targeted edit with a hash guard for the file path.
type Edit struct {
	Path         string `json:"path"`
	OriginalHash string `json:"original_hash"`
	NewContent   string `json:"new_content"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
}

// EditRequest is the structure passed to hashline_validate containing
// current file hashes and a list of edits to apply.
type EditRequest struct {
	Hashes map[string]string `json:"hashes"`
	Edits  []Edit            `json:"edits"`
}
