package provider

import (
	"errors"
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
		{"explicit opencode", NameOpencode, NameOpencode, ""},
		{"explicit cursor", NameCursor, NameCursor, ""},
		{"empty string is an explicit error", "", "", "no provider"},
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
		{"explicit opencode", NameOpencode, NameOpencode, false},
		{"explicit cursor", NameCursor, NameCursor, false},
		{"empty is an explicit error", "", "", true},
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

// TestEmptyNameYieldsErrNoProvider asserts the empty provider name no longer
// silently aliases to claude-code: both Get and NormalizeName return the
// sentinel ErrNoProvider so callers can match on it.
func TestEmptyNameYieldsErrNoProvider(t *testing.T) {
	p, err := Get("")
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("Get(\"\"): error = %v, want ErrNoProvider", err)
	}
	if p != nil {
		t.Fatalf("Get(\"\"): provider = %v, want nil", p)
	}

	name, err := NormalizeName("")
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("NormalizeName(\"\"): error = %v, want ErrNoProvider", err)
	}
	if name != "" {
		t.Fatalf("NormalizeName(\"\"): name = %q, want empty", name)
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
	_ Provider = Opencode{}
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
		{"opencode", Opencode{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.SupportsCommitLinking(); got != tt.want {
				t.Errorf("%s.SupportsCommitLinking() = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
