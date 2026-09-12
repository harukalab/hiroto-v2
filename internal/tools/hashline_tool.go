package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func registerHashline(r *Registry) {
	r.Register(&Tool{
		Name:        "hashline_validate",
		Description: "Validate that file content hashes match before applying edits. Reads each path from the request, computes current file hashes, compares against provided hashes, and returns validation results.",
		Parameters: mustJSON(`{
			"type":"object",
			"properties":{
				"request":{"type":"string","description":"JSON-serialized EditRequest with hashes and edits"},
				"workdir":{"type":"string","description":"Working directory for resolving relative paths"}
			},
			"required":["request"]
		}`),
		Exec: func(ctx context.Context, args map[string]any) Result {
			reqJSON, _ := args["request"].(string)
			if reqJSON == "" {
				return Result{Output: `{"valid":false,"mismatches":["missing request JSON"]}`, IsError: true}
			}

			var editReq EditRequest
			if err := json.Unmarshal([]byte(reqJSON), &editReq); err != nil {
				return Result{Output: fmt.Sprintf(`{"valid":false,"mismatches":["invalid JSON: %v"]}`, err), IsError: true}
			}

			workdir, _ := args["workdir"].(string)

			var mismatches []string
			currentHashes := make(map[string]string)

			// Validate each edit path by reading the file and computing hashes.
			seen := make(map[string]bool)
			for _, e := range editReq.Edits {
				if e.Path == "" {
					mismatches = append(mismatches, "empty path in edit")
					continue
				}
				if seen[e.Path] {
					continue // already processed
				}
				seen[e.Path] = true

				path := e.Path
				if !filepath.IsAbs(path) {
					if workdir != "" {
						path = filepath.Join(workdir, path)
					}
				}

				data, err := os.ReadFile(path)
				if err != nil {
					mismatches = append(mismatches, fmt.Sprintf("%s: read error: %v", e.Path, err))
					continue
				}
				content := string(data)

				// Compute the current hash for this path.
				currentHashes[e.Path] = computeFullFileHash(content)

				// If the provided hash doesn't match current file hash.
				providedHash, hasHash := editReq.Hashes[e.Path]
				if hasHash && providedHash != "" && providedHash != computeFullFileHash(content) {
					mismatches = append(mismatches, fmt.Sprintf("%s: hash mismatch (expected %s, got %s)", e.Path, providedHash, computeFullFileHash(content)))
				}
			}

			// Also validate edit line hashes against actual file content.
			editsByPath := make(map[string][]Edit)
			for _, e := range editReq.Edits {
				if e.Path != "" {
					editsByPath[e.Path] = append(editsByPath[e.Path], e)
				}
			}

			for path, pathEdits := range editsByPath {
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				content := string(data)

				if !validateEditHash(content, pathEdits) {
					for _, e := range pathEdits {
						if e.OriginalHash != "" {
							mismatches = append(mismatches,
								fmt.Sprintf("%s edit [%d-%d]: line hash mismatch (expected %s)", e.Path, e.StartLine, e.EndLine, e.OriginalHash))
						}
					}
				}
			}

			valid := len(mismatches) == 0

			result, _ := json.Marshal(map[string]any{
				"valid":       valid,
				"mismatches":  mismatches,
				"currentHashes": currentHashes,
			})
			return Result{Output: string(result)}
		},
	})
}
