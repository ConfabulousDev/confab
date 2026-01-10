package redactor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
)

// Redactor handles redaction of sensitive data
type Redactor struct {
	patterns []compiledPattern
}

// compiledPattern represents a compiled regex pattern with metadata
type compiledPattern struct {
	regex        *regexp.Regexp
	fieldRegex   *regexp.Regexp // nil means apply to all string values
	patternType  string
	captureGroup int
}

// NewRedactor creates a new Redactor from a config
func NewRedactor(cfg Config) (*Redactor, error) {
	return compilePatterns(cfg.Patterns)
}

// NewFromConfig creates a new Redactor from a config.RedactionConfig.
// Returns nil if cfg is nil or if no patterns are configured.
// Note: This function does NOT check cfg.Enabled - callers should check that.
// If UseDefaultPatterns is true (default), default patterns are included.
// Custom patterns from cfg.Patterns are added after default patterns.
func NewFromConfig(cfg *config.RedactionConfig) (*Redactor, error) {
	if cfg == nil {
		return nil, nil
	}

	var patterns []Pattern

	// Add default patterns if enabled (default behavior)
	if cfg.ShouldUseDefaultPatterns() {
		for _, p := range config.GetDefaultRedactionPatterns() {
			patterns = append(patterns, Pattern{
				Name:         p.Name,
				Pattern:      p.Pattern,
				Type:         p.Type,
				CaptureGroup: p.CaptureGroup,
				FieldPattern: p.FieldPattern,
			})
		}
	}

	// Add custom patterns from config
	for _, p := range cfg.Patterns {
		patterns = append(patterns, Pattern{
			Name:         p.Name,
			Pattern:      p.Pattern,
			Type:         p.Type,
			CaptureGroup: p.CaptureGroup,
			FieldPattern: p.FieldPattern,
		})
	}

	// Return nil if no patterns to apply
	if len(patterns) == 0 {
		return nil, nil
	}

	return compilePatterns(patterns)
}

// compilePatterns compiles a list of patterns into a Redactor
func compilePatterns(patterns []Pattern) (*Redactor, error) {
	compiled := make([]compiledPattern, 0, len(patterns))

	for _, p := range patterns {
		cp := compiledPattern{
			patternType:  p.Type,
			captureGroup: p.CaptureGroup,
		}

		// Compile value pattern if provided
		if p.Pattern != "" {
			regex, err := regexp.Compile(p.Pattern)
			if err != nil {
				return nil, fmt.Errorf("failed to compile pattern '%s': %w", p.Name, err)
			}
			cp.regex = regex
		}

		// Compile field pattern if provided
		if p.FieldPattern != "" {
			fieldRegex, err := regexp.Compile(p.FieldPattern)
			if err != nil {
				return nil, fmt.Errorf("failed to compile field pattern '%s': %w", p.Name, err)
			}
			cp.fieldRegex = fieldRegex
		}

		// Validate: must have at least one of Pattern or FieldPattern
		if cp.regex == nil && cp.fieldRegex == nil {
			return nil, fmt.Errorf("pattern '%s' must have either pattern or field_pattern", p.Name)
		}

		compiled = append(compiled, cp)
	}

	return &Redactor{
		patterns: compiled,
	}, nil
}

// Redact redacts sensitive data from a string using value-based patterns only.
// Field-based patterns are skipped since plain text has no field context.
func (r *Redactor) Redact(input string) string {
	result := input

	for _, p := range r.patterns {
		// Skip field-based patterns for plain text (no field context)
		if p.fieldRegex != nil {
			continue
		}
		// Skip patterns without a value regex
		if p.regex == nil {
			continue
		}
		if p.captureGroup > 0 {
			// Partial redaction using capture group
			result = r.redactCaptureGroup(result, p)
		} else {
			// Full match redaction
			result = r.redactFullMatch(result, p)
		}
	}

	return result
}

// RedactJSONL redacts sensitive data from JSONL content by parsing each line,
// recursively redacting string values, and re-serializing. This ensures JSON
// structure is never corrupted by redaction patterns.
func (r *Redactor) RedactJSONL(input []byte) []byte {
	var result bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(input))

	// Handle large lines (transcripts can have big content blocks)
	const maxLineSize = 10 * 1024 * 1024 // 10MB
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)

	first := true
	for scanner.Scan() {
		line := scanner.Bytes()

		// Preserve empty lines as-is
		if len(bytes.TrimSpace(line)) == 0 {
			if !first {
				result.WriteByte('\n')
			}
			first = false
			continue
		}

		// Parse JSON
		var data interface{}
		if err := json.Unmarshal(line, &data); err != nil {
			// If parsing fails, fall back to text-based redaction
			if !first {
				result.WriteByte('\n')
			}
			result.Write([]byte(r.Redact(string(line))))
			first = false
			continue
		}

		// Recursively redact string values
		redacted := r.redactValueWithFieldContext(data, "")

		// Re-serialize
		output, err := json.Marshal(redacted)
		if err != nil {
			// Shouldn't happen, but fall back to original if it does
			if !first {
				result.WriteByte('\n')
			}
			result.Write(line)
			first = false
			continue
		}

		if !first {
			result.WriteByte('\n')
		}
		result.Write(output)
		first = false
	}

	return result.Bytes()
}

// RedactJSONLine redacts a single JSON line, parsing it and applying redaction
// to string values only. Returns the redacted JSON. If the input is not valid
// JSON, falls back to text-based redaction.
func (r *Redactor) RedactJSONLine(line string) string {
	// Try to parse as JSON
	var data interface{}
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		// Not valid JSON, fall back to text-based redaction (value patterns only)
		return r.redactTextValuePatternsOnly(line)
	}

	// Recursively redact string values
	redacted := r.redactValueWithFieldContext(data, "")

	// Re-serialize
	output, err := json.Marshal(redacted)
	if err != nil {
		// Shouldn't happen, but fall back to original if it does
		return line
	}

	return string(output)
}

// redactTextValuePatternsOnly applies only value-based patterns (no field patterns)
// to plain text. This is the safe fallback for non-JSON content.
func (r *Redactor) redactTextValuePatternsOnly(input string) string {
	result := input
	for _, p := range r.patterns {
		// Skip field-based patterns for text mode
		if p.fieldRegex != nil {
			continue
		}
		if p.regex == nil {
			continue
		}
		if p.captureGroup > 0 {
			result = r.redactCaptureGroup(result, p)
		} else {
			result = r.redactFullMatch(result, p)
		}
	}
	return result
}

// redactValueWithFieldContext recursively redacts string values in a JSON structure,
// tracking the current field name for field-based pattern matching.
func (r *Redactor) redactValueWithFieldContext(v interface{}, fieldName string) interface{} {
	switch val := v.(type) {
	case string:
		return r.redactStringValue(val, fieldName)
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, v := range val {
			result[k] = r.redactValueWithFieldContext(v, k)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, v := range val {
			// Array elements inherit parent field name for field-based matching
			result[i] = r.redactValueWithFieldContext(v, fieldName)
		}
		return result
	default:
		// Numbers, bools, null - return as-is
		return val
	}
}

// redactStringValue applies redaction patterns to a string value, considering
// both value-based and field-based patterns.
func (r *Redactor) redactStringValue(value, fieldName string) string {
	result := value

	for _, p := range r.patterns {
		if p.fieldRegex != nil {
			// Field-based pattern: only apply if field name matches
			if fieldName == "" || !p.fieldRegex.MatchString(fieldName) {
				continue
			}
			// Field matches - redact the value
			if p.regex != nil {
				// Apply value regex to matching field
				if p.captureGroup > 0 {
					result = r.redactCaptureGroup(result, p)
				} else {
					result = r.redactFullMatch(result, p)
				}
			} else {
				// No value regex - redact entire value
				result = fmt.Sprintf("[REDACTED:%s]", strings.ToUpper(p.patternType))
			}
		} else if p.regex != nil {
			// Value-based pattern: apply to all string values
			if p.captureGroup > 0 {
				result = r.redactCaptureGroup(result, p)
			} else {
				result = r.redactFullMatch(result, p)
			}
		}
	}

	return result
}

// redactFullMatch replaces the entire match with a redaction marker
func (r *Redactor) redactFullMatch(input string, p compiledPattern) string {
	marker := fmt.Sprintf("[REDACTED:%s]", strings.ToUpper(p.patternType))
	return p.regex.ReplaceAllString(input, marker)
}

// redactCaptureGroup replaces only the specified capture group
func (r *Redactor) redactCaptureGroup(input string, p compiledPattern) string {
	marker := fmt.Sprintf("[REDACTED:%s]", strings.ToUpper(p.patternType))

	return p.regex.ReplaceAllStringFunc(input, func(match string) string {
		// Use FindStringSubmatchIndex to get exact positions of capture groups.
		// This is more reliable than strings.Index, which would find the first
		// occurrence of the captured text (wrong if it appears multiple times).
		indices := p.regex.FindStringSubmatchIndex(match)
		if len(indices) <= p.captureGroup*2+1 {
			// If capture group doesn't exist, return original match
			return match
		}

		// Get the start and end positions of the capture group
		start := indices[p.captureGroup*2]
		end := indices[p.captureGroup*2+1]
		if start == -1 || end == -1 {
			// Capture group didn't participate in match
			return match
		}

		return match[:start] + marker + match[end:]
	})
}
