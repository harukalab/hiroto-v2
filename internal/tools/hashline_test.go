package tools

import (
	"testing"
)

func TestComputeLineHashConsistency(t *testing.T) {
	content := "hello world"
	h1 := computeLineHash(content)
	h2 := computeLineHash(content)
	if h1 != h2 {
		t.Errorf("same content produced different hashes: %q vs %q", h1, h2)
	}
	if len(h1) != 4 {
		t.Errorf("line hash should be 2 hex bytes (4 chars), got %d: %q", len(h1), h1)
	}
}

func TestComputeFullFileHashConsistency(t *testing.T) {
	content := "package main\n\nfunc main() {}\n"
	h1 := computeFullFileHash(content)
	h2 := computeFullFileHash(content)
	if h1 != h2 {
		t.Errorf("same content produced different hashes: %q vs %q", h1, h2)
	}
	if len(h1) != 6 {
		t.Errorf("full file hash should be 3 hex bytes (6 chars), got %d: %q", len(h1), h1)
	}
}

func TestComputeHashesDiffer(t *testing.T) {
	h1 := computeLineHash("hello")
	h2 := computeLineHash("world")
	if h1 == h2 {
		t.Errorf("different content produced same line hash: %q", h1)
	}
}

func TestValidateEditHashAllMatch(t *testing.T) {
	content := "line one\nline two\nline three\n"
	edits := []Edit{
		{OriginalHash: computeLineHash("line one"), StartLine: 0, EndLine: 1},
		{OriginalHash: computeLineHash("line two"), StartLine: 1, EndLine: 2},
	}
	if !validateEditHash(content, edits) {
		t.Error("expected all edits to validate")
	}
}

func TestValidateEditHashOneMismatch(t *testing.T) {
	content := "line one\nline two\nline three\n"
	edits := []Edit{
		{OriginalHash: computeLineHash("line one"), StartLine: 0, EndLine: 1},
		{OriginalHash: "deadbeef", StartLine: 1, EndLine: 2},
	}
	if validateEditHash(content, edits) {
		t.Error("expected validation to fail on bad hash")
	}
}

func TestValidateEditHashEmptyOriginalHash(t *testing.T) {
	content := "line one\nline two\n"
	edits := []Edit{
		{OriginalHash: "", StartLine: 0, EndLine: 1}, // no guard
	}
	if !validateEditHash(content, edits) {
		t.Error("expected validation to pass when original_hash is empty")
	}
}

func TestValidateEditHashOutOfBounds(t *testing.T) {
	content := "line one\n"
	edits := []Edit{
		{OriginalHash: "abcdef", StartLine: 0, EndLine: 10},
	}
	if validateEditHash(content, edits) {
		t.Error("expected validation to fail on out-of-bounds range")
	}
}
