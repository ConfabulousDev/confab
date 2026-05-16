package provider

import (
	"strings"
	"testing"
)

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

// TestProviderInterfaceSatisfaction ensures both concrete providers
// satisfy the Provider interface. Compile-time assertions in claude.go
// and codex.go are the primary check; this test exists so the contract
// is also visible in the test report.
func TestProviderInterfaceSatisfaction(t *testing.T) {
	var _ Provider = ClaudeCode{}
	var _ Provider = Codex{}
}
