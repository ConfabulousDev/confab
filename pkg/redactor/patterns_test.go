package redactor

import (
	"regexp"
	"testing"
)

// TestAnthropicAPIKeyPattern tests the Anthropic API key pattern
func TestAnthropicAPIKeyPattern(t *testing.T) {
	pattern := `sk-ant-api\d{2}-[A-Za-z0-9_-]{80,120}`
	re := regexp.MustCompile(pattern)

	// True positives - should match (80-120 chars after prefix)
	validKeys := []string{
		// 95 chars (original length)
		"sk-ant-api03-1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-1234567890abcdefghijklm",
		"sk-ant-api01-aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789_-aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789_-aBcDeFgHiJkLmNoPqRs",
		// 80 chars (minimum)
		"sk-ant-api03-" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-abcdefghijklmnop",
		// 120 chars (maximum)
		"sk-ant-api03-" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
	}

	for _, key := range validKeys {
		if !re.MatchString(key) {
			t.Errorf("Pattern should match valid Anthropic API key: %s", key)
		}
	}

	// False positives - should NOT match
	invalidKeys := []string{
		"sk-ant-api-short",                         // Too short
		"sk-ant-api99-",                            // Missing characters
		"sk-ant-apix3-" + string(make([]byte, 95)), // Invalid format (apix not api\d\d)
		"sk-openai-1234567890",                     // Different API key format
		"not-an-api-key",                           // Random string
		"sk-ant-api",                               // Incomplete
		"sk-ant-api03-short",                       // Less than 80 chars
	}

	for _, key := range invalidKeys {
		if re.MatchString(key) {
			t.Errorf("Pattern should NOT match invalid key: %s", key)
		}
	}
}

// TestOpenAIAPIKeyPattern tests the OpenAI API key pattern
func TestOpenAIAPIKeyPattern(t *testing.T) {
	pattern := `sk-(?:proj-)?[A-Za-z0-9_-]{20,200}`
	re := regexp.MustCompile(pattern)

	// True positives - should match
	validKeys := []string{
		// Legacy format (51 chars total)
		"sk-1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL",
		"sk-aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789aBcDeFgHiJkL",
		// New project-based format (variable length, typically 156+ chars)
		"sk-proj-abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"sk-proj-" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-abcdefghijklmnopqrstuvwxyz",
	}

	for _, key := range validKeys {
		if !re.MatchString(key) {
			t.Errorf("Pattern should match valid OpenAI API key: %s", key)
		}
	}

	// False positives - should NOT match
	invalidKeys := []string{
		"sk-short",                           // Too short (less than 20 chars after prefix)
		"sk-proj-short",                      // Too short (less than 20 chars after prefix)
		"sk-abc!@#$%^&*()",                   // Invalid characters
		"not-sk-prefix",                      // Wrong prefix
	}

	for _, key := range invalidKeys {
		if re.MatchString(key) {
			t.Errorf("Pattern should NOT match invalid key: %s", key)
		}
	}
}

// TestAWSAccessKeyPattern tests the AWS access key pattern
func TestAWSAccessKeyPattern(t *testing.T) {
	pattern := `AKIA[0-9A-Z]{16}`
	re := regexp.MustCompile(pattern)

	// True positives - should match
	validKeys := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"AKIABCDEFGHIJKLMNOPQ",
		"AKIA1234567890ABCDEF",
	}

	for _, key := range validKeys {
		if !re.MatchString(key) {
			t.Errorf("Pattern should match valid AWS access key: %s", key)
		}
	}

	// False positives - should NOT match
	invalidKeys := []string{
		"AKIA123",              // Too short
		"AKIAIOSFODNN7example", // Lowercase not allowed
		"BKIAIOSFODNN7EXAMPLE", // Wrong prefix
		"AKIAabcdefghijklmnop", // Lowercase letters
	}

	for _, key := range invalidKeys {
		if re.MatchString(key) {
			t.Errorf("Pattern should NOT match invalid key: %s", key)
		}
	}
}

// TestGitHubTokenPattern tests the GitHub personal access token patterns
func TestGitHubTokenPattern(t *testing.T) {
	testCases := []struct {
		name    string
		pattern string
		valid   []string
		invalid []string
	}{
		{
			name:    "Classic PAT (ghp_)",
			pattern: `ghp_[A-Za-z0-9]{36,255}`,
			valid: []string{
				"ghp_1234567890abcdefghijklmnopqrstuvwxyz",
				"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789",
				// Longer token (future-proofing)
				"ghp_1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
			},
			invalid: []string{
				"ghp_short",        // Too short
				"ghp_12345",        // Too short
				"not-github-token", // No prefix
			},
		},
		{
			name:    "Fine-grained PAT (github_pat_)",
			pattern: `github_pat_[A-Za-z0-9]{22,255}`,
			valid: []string{
				// Fine-grained PATs are typically 82 chars after prefix (93 total)
				"github_pat_11ABCDEFGH0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
				"github_pat_abcdefghijklmnopqrstuv",
			},
			invalid: []string{
				"github_pat_short",  // Too short
				"github_pat_abc",    // Too short
				"ghp_1234567890abc", // Wrong prefix
			},
		},
		{
			name:    "OAuth Token (gho_)",
			pattern: `gho_[A-Za-z0-9]{36,255}`,
			valid: []string{
				"gho_1234567890abcdefghijklmnopqrstuvwxyz",
				"gho_1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
			},
			invalid: []string{
				"gho_short",        // Too short
				"ghp_1234567890abc", // Wrong prefix
			},
		},
		{
			name:    "App Token (ghu_/ghs_)",
			pattern: `(?:ghu|ghs)_[A-Za-z0-9]{36,255}`,
			valid: []string{
				"ghu_1234567890abcdefghijklmnopqrstuvwxyz",
				"ghs_1234567890abcdefghijklmnopqrstuvwxyz",
				"ghu_1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
			},
			invalid: []string{
				"ghu_short",         // Too short
				"ghs_short",         // Too short
				"ghx_1234567890abc", // Wrong prefix
			},
		},
		{
			name:    "Refresh Token (ghr_)",
			pattern: `ghr_[A-Za-z0-9]{36,255}`,
			valid: []string{
				"ghr_1234567890abcdefghijklmnopqrstuvwxyz",
				"ghr_1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
			},
			invalid: []string{
				"ghr_short",        // Too short
				"ghp_1234567890abc", // Wrong prefix
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)

			for _, token := range tc.valid {
				if !re.MatchString(token) {
					t.Errorf("Pattern should match valid token: %s", token)
				}
			}

			for _, token := range tc.invalid {
				if re.MatchString(token) {
					t.Errorf("Pattern should NOT match invalid token: %s", token)
				}
			}
		})
	}
}

// TestJWTPattern tests the JWT token pattern
func TestJWTPattern(t *testing.T) {
	pattern := `eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`
	re := regexp.MustCompile(pattern)

	// True positives - should match
	validJWTs := []string{
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		"eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJodHRwczovL2V4YW1wbGUuY29tIn0.signature",
	}

	for _, jwt := range validJWTs {
		if !re.MatchString(jwt) {
			t.Errorf("Pattern should match valid JWT: %s", jwt)
		}
	}

	// False positives - should NOT match
	invalidJWTs := []string{
		"not.a.jwt",           // No eyJ prefix
		"eyJ.eyJ.",            // Incomplete
		"eyJtest",             // No dots
		"random string",       // Random text
	}

	for _, jwt := range invalidJWTs {
		if re.MatchString(jwt) {
			t.Errorf("Pattern should NOT match invalid JWT: %s", jwt)
		}
	}
}

// TestPrivateKeyPattern tests the private key patterns match the full key body
func TestPrivateKeyPattern(t *testing.T) {
	testCases := []struct {
		name    string
		pattern string
		valid   []string
		invalid []string
	}{
		{
			name:    "RSA Private Key",
			pattern: `(?s)-----BEGIN RSA PRIVATE KEY-----.*?-----END RSA PRIVATE KEY-----`,
			valid: []string{
				"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA0Z3...\nbase64content\n-----END RSA PRIVATE KEY-----",
				"-----BEGIN RSA PRIVATE KEY-----\nshort\n-----END RSA PRIVATE KEY-----",
			},
			invalid: []string{
				"-----BEGIN RSA PRIVATE KEY-----",           // Missing END
				"-----BEGIN RSA PRIVATE KEY-----\nMIIE...",  // Incomplete
				"-----BEGIN PUBLIC KEY-----\ndata\n-----END PUBLIC KEY-----", // Public key
			},
		},
		{
			name:    "EC Private Key",
			pattern: `(?s)-----BEGIN EC PRIVATE KEY-----.*?-----END EC PRIVATE KEY-----`,
			valid: []string{
				"-----BEGIN EC PRIVATE KEY-----\nMHQCAQ...\n-----END EC PRIVATE KEY-----",
			},
			invalid: []string{
				"-----BEGIN EC PRIVATE KEY-----", // Missing END
			},
		},
		{
			name:    "OpenSSH Private Key",
			pattern: `(?s)-----BEGIN OPENSSH PRIVATE KEY-----.*?-----END OPENSSH PRIVATE KEY-----`,
			valid: []string{
				"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNza...\n-----END OPENSSH PRIVATE KEY-----",
			},
			invalid: []string{
				"-----BEGIN OPENSSH PRIVATE KEY-----", // Missing END
			},
		},
		{
			name:    "Generic Private Key (PKCS#8)",
			pattern: `(?s)-----BEGIN PRIVATE KEY-----.*?-----END PRIVATE KEY-----`,
			valid: []string{
				"-----BEGIN PRIVATE KEY-----\nMIIEvQIBAD...\n-----END PRIVATE KEY-----",
			},
			invalid: []string{
				"-----BEGIN PRIVATE KEY-----",         // Missing END
				"-----BEGIN RSA PRIVATE KEY-----\ndata\n-----END RSA PRIVATE KEY-----", // Different type
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)

			for _, key := range tc.valid {
				if !re.MatchString(key) {
					t.Errorf("Pattern should match valid key:\n%s", key)
				}
			}

			for _, key := range tc.invalid {
				if re.MatchString(key) {
					t.Errorf("Pattern should NOT match invalid key:\n%s", key)
				}
			}
		})
	}
}

// TestPostgreSQLConnectionStringPattern tests the PostgreSQL connection string pattern
func TestPostgreSQLConnectionStringPattern(t *testing.T) {
	pattern := `postgres(?:ql)?://[^:]+:([^@\s]+)@`
	re := regexp.MustCompile(pattern)

	// True positives - should match and capture password
	validConnStrings := []string{
		"postgres://user:password123@localhost:5432/db",
		"postgresql://admin:secret@db.example.com/mydb",
		"postgres://dbuser:my-p@ssw0rd@127.0.0.1:5432/",
	}

	for _, connStr := range validConnStrings {
		matches := re.FindStringSubmatch(connStr)
		if matches == nil {
			t.Errorf("Pattern should match valid PostgreSQL connection string: %s", connStr)
		}
		if len(matches) < 2 {
			t.Errorf("Pattern should capture password from connection string: %s", connStr)
		}
	}

	// False positives - should NOT match
	invalidConnStrings := []string{
		"mysql://user:password@localhost/db",  // Different database
		"postgres://localhost:5432/db",         // No password
		"http://user:pass@example.com",         // Not a database connection
	}

	for _, connStr := range invalidConnStrings {
		if re.MatchString(connStr) {
			t.Errorf("Pattern should NOT match non-PostgreSQL string: %s", connStr)
		}
	}
}

// TestGenericPasswordPattern tests a generic password pattern for URLs
func TestGenericPasswordPattern(t *testing.T) {
	pattern := `://[^:/@\s]+:([^@\s]+)@`
	re := regexp.MustCompile(pattern)

	// True positives - should match and capture password
	validURLs := []string{
		"https://user:password@example.com",
		"ftp://admin:secret123@ftp.example.com",
		"redis://default:mypassword@redis.local:6379",
	}

	for _, url := range validURLs {
		matches := re.FindStringSubmatch(url)
		if matches == nil {
			t.Errorf("Pattern should match URL with credentials: %s", url)
		}
		if len(matches) < 2 {
			t.Errorf("Pattern should capture password from URL: %s", url)
		}
	}

	// False positives - should NOT match
	invalidURLs := []string{
		"https://example.com",              // No credentials
		"user:password",                    // No URL format
		"://no-user@example.com",          // No user
	}

	for _, url := range invalidURLs {
		if re.MatchString(url) {
			t.Errorf("Pattern should NOT match URL without proper credentials: %s", url)
		}
	}
}

// TestSlackTokenPattern tests the Slack token patterns
func TestSlackTokenPattern(t *testing.T) {
	testCases := []struct {
		name    string
		pattern string
		valid   []string
		invalid []string
	}{
		{
			name:    "Standard Slack tokens (xox[baprs]-)",
			pattern: `xox[baprs]-[0-9a-zA-Z-]{10,255}`,
			valid: []string{
				"xoxb-1234567890-1234567890-abcdefghijklmnopqrstuvwx",
				"xoxp-1234567890-1234567890-1234567890-abc",
				"xoxa-1234567890",
				"xoxr-abcdefghij",
				"xoxs-1234567890-1234567890-1234567890-abcdefghijklmnopqrstuvwxyz",
			},
			invalid: []string{
				"xoxb-short",      // Too short
				"xoxx-1234567890", // Invalid type
				"not-slack-token", // No prefix
			},
		},
		{
			name:    "Rotating Slack tokens (xoxe-)",
			pattern: `xoxe(?:\.[a-zA-Z0-9-]+)?-[0-9a-zA-Z-]{10,255}`,
			valid: []string{
				"xoxe-1-abcdefghij",
				"xoxe.xoxb-1-1234567890-abcdefghijklmnopqrstuvwxyz",
				"xoxe.xoxp-1-1234567890-1234567890-abcdefghijklmnop",
			},
			invalid: []string{
				"xoxe-short",      // Too short
				"xoxe-",           // Empty
				"not-slack-token", // No prefix
			},
		},
		{
			name:    "App-level Slack tokens (xapp-)",
			pattern: `xapp-[0-9a-zA-Z-]{10,255}`,
			valid: []string{
				"xapp-1-abcdefghij",
				"xapp-1234567890-1234567890-abcdefghijklmnopqrstuvwxyz",
			},
			invalid: []string{
				"xapp-short",      // Too short
				"xapp-",           // Empty
				"not-slack-token", // No prefix
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)

			for _, token := range tc.valid {
				if !re.MatchString(token) {
					t.Errorf("Pattern should match valid token: %s", token)
				}
			}

			for _, token := range tc.invalid {
				if re.MatchString(token) {
					t.Errorf("Pattern should NOT match invalid token: %s", token)
				}
			}
		})
	}
}

// TestStripeAPIKeyPattern tests the Stripe API key patterns
func TestStripeAPIKeyPattern(t *testing.T) {
	testCases := []struct {
		name    string
		pattern string
		valid   []string
		invalid []string
	}{
		{
			name:    "Stripe Secret Keys (sk_live/sk_test)",
			pattern: `sk_(?:live|test)_[0-9a-zA-Z]{24,}`,
			valid: []string{
				"sk_live_1234567890abcdefghijklmnopqrstuvwxyz",
				"sk_live_aBcDeFgHiJkLmNoPqRsTuVwXyZ",
				"sk_test_1234567890abcdefghijklmnopqrstuvwxyz",
				"sk_test_aBcDeFgHiJkLmNoPqRsTuVwXyZ",
			},
			invalid: []string{
				"sk_live_short",       // Too short
				"sk_test_short",       // Too short
				"pk_live_1234567890",  // Publishable key, not secret
				"sk_prod_1234567890",  // Invalid environment
			},
		},
		{
			name:    "Stripe Restricted Keys (rk_live/rk_test)",
			pattern: `rk_(?:live|test)_[0-9a-zA-Z]{24,}`,
			valid: []string{
				"rk_live_1234567890abcdefghijklmnopqrstuvwxyz",
				"rk_test_1234567890abcdefghijklmnopqrstuvwxyz",
			},
			invalid: []string{
				"rk_live_short",       // Too short
				"sk_live_1234567890",  // Wrong prefix (sk not rk)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)

			for _, key := range tc.valid {
				if !re.MatchString(key) {
					t.Errorf("Pattern should match valid key: %s", key)
				}
			}

			for _, key := range tc.invalid {
				if re.MatchString(key) {
					t.Errorf("Pattern should NOT match invalid key: %s", key)
				}
			}
		})
	}
}

// TestGoogleAPIKeyPattern tests the Google API key pattern
func TestGoogleAPIKeyPattern(t *testing.T) {
	pattern := `AIza[0-9A-Za-z_-]{35}`
	re := regexp.MustCompile(pattern)

	// True positives - should match
	validKeys := []string{
		"AIzaSyDaGmWKa4JsXZ-HjGw7ISLn_3namBGewQe",
		"AIzaBCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
	}

	for _, key := range validKeys {
		if !re.MatchString(key) {
			t.Errorf("Pattern should match valid Google API key: %s", key)
		}
	}

	// False positives - should NOT match
	invalidKeys := []string{
		"AIza123",                          // Too short
		"AIzaBCDEFGHIJ!@#$%^&*()",         // Invalid characters
		"NotAIza1234567890123456789012345", // Wrong prefix
	}

	for _, key := range invalidKeys {
		if re.MatchString(key) {
			t.Errorf("Pattern should NOT match invalid key: %s", key)
		}
	}
}

// TestConfabAPIKeyPattern tests the Confab API key pattern
func TestConfabAPIKeyPattern(t *testing.T) {
	pattern := `cfb_[A-Za-z0-9]{40}`
	re := regexp.MustCompile(pattern)

	// True positives - should match
	validKeys := []string{
		"cfb_7TqGbZ4ms5sMNUUQBJ2Ekocob6ARRK4qQiUcm24G",
		"cfb_abcdefghijklmnopqrstuvwxyz12345678901234",
		"cfb_ABCDEFGHIJKLMNOPQRSTUVWXYZ12345678901234",
		"cfb_0123456789012345678901234567890123456789",
	}

	for _, key := range validKeys {
		if !re.MatchString(key) {
			t.Errorf("Pattern should match valid Confab API key: %s", key)
		}
	}

	// False positives - should NOT match
	invalidKeys := []string{
		"cfb_short",         // Too short
		"cfb_abc!@#$%^&*()", // Invalid characters
		"not-cfb-prefix",    // Wrong prefix
		"cfb_123",           // Too short
		"cfb_",              // Missing random part
	}

	for _, key := range invalidKeys {
		if re.MatchString(key) {
			t.Errorf("Pattern should NOT match invalid key: %s", key)
		}
	}
}
