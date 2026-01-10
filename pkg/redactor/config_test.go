package redactor

import (
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
)

// TestGetDefaultRedactionPatterns tests that default patterns are valid
func TestGetDefaultRedactionPatterns(t *testing.T) {
	patterns := config.GetDefaultRedactionPatterns()

	// Should have multiple default patterns
	if len(patterns) < 5 {
		t.Errorf("Expected at least 5 default patterns, got %d", len(patterns))
	}

	// Verify pattern structure
	for i, pattern := range patterns {
		if pattern.Name == "" {
			t.Errorf("Pattern %d has empty name", i)
		}
		// Must have at least one of Pattern or FieldPattern
		if pattern.Pattern == "" && pattern.FieldPattern == "" {
			t.Errorf("Pattern %d (%s) has neither pattern nor field_pattern", i, pattern.Name)
		}
		if pattern.Type == "" {
			t.Errorf("Pattern %d (%s) has empty type", i, pattern.Name)
		}
	}
}

// TestDefaultPatternsCanCompile tests that all default patterns can be compiled
func TestDefaultPatternsCanCompile(t *testing.T) {
	// Use NewFromConfig which loads default patterns
	redactor, err := NewFromConfig(&config.RedactionConfig{
		Enabled: true,
		// UseDefaultPatterns defaults to true
	})
	if err != nil {
		t.Fatalf("Failed to compile default patterns: %v", err)
	}

	if redactor == nil {
		t.Error("Expected non-nil redactor")
	}
}

// TestUseDefaultPatternsFlag tests the use_default_patterns behavior
func TestUseDefaultPatternsFlag(t *testing.T) {
	t.Run("defaults to true when nil", func(t *testing.T) {
		redactor, err := NewFromConfig(&config.RedactionConfig{
			Enabled: true,
			// UseDefaultPatterns is nil, should default to true
		})
		if err != nil {
			t.Fatalf("Failed to create redactor: %v", err)
		}
		if redactor == nil {
			t.Error("Expected non-nil redactor when use_default_patterns defaults to true")
		}
	})

	t.Run("false with no custom patterns returns nil", func(t *testing.T) {
		useDefaults := false
		redactor, err := NewFromConfig(&config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns:           []config.RedactionPattern{},
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if redactor != nil {
			t.Error("Expected nil redactor when use_default_patterns is false and no custom patterns")
		}
	})

	t.Run("false with custom patterns uses only custom", func(t *testing.T) {
		useDefaults := false
		redactor, err := NewFromConfig(&config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns: []config.RedactionPattern{
				{Name: "Custom", Pattern: `CUSTOM_[A-Z]+`, Type: "custom"},
			},
		})
		if err != nil {
			t.Fatalf("Failed to create redactor: %v", err)
		}
		if redactor == nil {
			t.Fatal("Expected non-nil redactor with custom patterns")
		}

		// Should redact custom pattern
		result := redactor.Redact("Key: CUSTOM_SECRET")
		if result != "Key: [REDACTED:CUSTOM]" {
			t.Errorf("Expected custom pattern to be redacted, got: %s", result)
		}

		// Should NOT redact default patterns (e.g., OpenAI key)
		openaiKey := "sk-1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"
		result = redactor.Redact(openaiKey)
		if result != openaiKey {
			t.Errorf("Expected OpenAI key to NOT be redacted when use_default_patterns=false, got: %s", result)
		}
	})

	t.Run("true with custom patterns uses both", func(t *testing.T) {
		useDefaults := true
		redactor, err := NewFromConfig(&config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns: []config.RedactionPattern{
				{Name: "Custom", Pattern: `CUSTOM_[A-Z]+`, Type: "custom"},
			},
		})
		if err != nil {
			t.Fatalf("Failed to create redactor: %v", err)
		}
		if redactor == nil {
			t.Fatal("Expected non-nil redactor")
		}

		// Should redact custom pattern
		result := redactor.Redact("Key: CUSTOM_SECRET")
		if result != "Key: [REDACTED:CUSTOM]" {
			t.Errorf("Expected custom pattern to be redacted, got: %s", result)
		}

		// Should also redact default patterns (e.g., OpenAI key)
		openaiKey := "sk-1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"
		result = redactor.Redact(openaiKey)
		if result == openaiKey {
			t.Error("Expected OpenAI key to be redacted when use_default_patterns=true")
		}
	})
}

// TestDefaultPatternsHaveExpectedTypes tests that expected pattern types exist
func TestDefaultPatternsHaveExpectedTypes(t *testing.T) {
	patterns := config.GetDefaultRedactionPatterns()

	expectedTypes := map[string]bool{
		"api_key":         false,
		"github_token":    false,
		"jwt":             false,
		"private_key":     false,
		"password":        false,
		"sensitive_field": false,
	}

	for _, pattern := range patterns {
		if _, ok := expectedTypes[pattern.Type]; ok {
			expectedTypes[pattern.Type] = true
		}
	}

	for patternType, found := range expectedTypes {
		if !found {
			t.Errorf("Expected pattern type %q not found in default patterns", patternType)
		}
	}
}
