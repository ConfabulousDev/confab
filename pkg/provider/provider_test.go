package provider

import (
	"strings"
	"testing"
)

// See bottom of file for compile-time Provider interface checks.

func TestGet(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantName  string
		wantError string
	}{
		{"explicit claude-code", NameClaudeCode, NameClaudeCode, ""},
		{"explicit codex", NameCodex, NameCodex, ""},
		{"empty string defaults to claude-code", "", NameClaudeCode, ""},
		{"unknown provider returns error", "openai", "", "unsupported provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Get(tt.input)
			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("Get(%q): want error containing %q, got nil", tt.input, tt.wantError)
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("Get(%q): error = %q, want substring %q", tt.input, err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get(%q): unexpected error: %v", tt.input, err)
			}
			if p == nil {
				t.Fatalf("Get(%q): provider is nil", tt.input)
			}
			if p.Name() != tt.wantName {
				t.Fatalf("Get(%q).Name() = %q, want %q", tt.input, p.Name(), tt.wantName)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{"explicit claude-code", NameClaudeCode, NameClaudeCode, false},
		{"explicit codex", NameCodex, NameCodex, false},
		{"empty defaults to claude-code", "", NameClaudeCode, false},
		{"unknown provider errors", "openai", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeName(tt.input)
			if tt.wantError {
				if err == nil {
					t.Fatalf("NormalizeName(%q): want error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeName(%q): unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// Package-level compile-time interface satisfaction checks. These
// ensure each Provider implementation continues to satisfy the
// interface; the Go compiler enforces the assertion at build time.
// They are intentionally NOT wrapped in a test function — the previous
// TestProviderInterfaceSatisfaction had an empty body and gave the
// false impression of runtime verification.
var (
	_ Provider = ClaudeCode{}
	_ Provider = Codex{}
)

// TestSupportsCommitLinking pins the contract that both currently-shipped
// providers advertise GitHub-link support. Adding a new provider that
// returns false here is fine — cmd/ handlers no-op cleanly for it — but
// the two existing providers must both stay true.
func TestSupportsCommitLinking(t *testing.T) {
	tests := []struct {
		name string
		p    Provider
		want bool
	}{
		{"claude-code", ClaudeCode{}, true},
		{"codex", Codex{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.SupportsCommitLinking(); got != tt.want {
				t.Errorf("%s.SupportsCommitLinking() = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
