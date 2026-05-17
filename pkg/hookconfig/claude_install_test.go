package hookconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
)

// makeHook creates a hook map with type and command.
func makeHook(hookType, command string) map[string]any {
	return map[string]any{"type": hookType, "command": command}
}

// makeMatcher creates a matcher with the given matcher string and hooks.
func makeMatcher(matcher string, hooks ...map[string]any) map[string]any {
	hooksList := make([]any, len(hooks))
	for i, h := range hooks {
		hooksList[i] = h
	}
	return map[string]any{"matcher": matcher, "hooks": hooksList}
}

// setTestHook installs a hook for an event via raw map manipulation.
func setTestHook(settings *config.ClaudeSettings, eventName string, matchers ...map[string]any) {
	matchersList := make([]any, len(matchers))
	for i, m := range matchers {
		matchersList[i] = m
	}
	if err := settings.SetEventHooks(eventName, matchersList); err != nil {
		panic("setTestHook: " + err.Error())
	}
}

func TestIsConfabCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{
			name:    "full path with save",
			command: "/usr/local/bin/confab save",
			want:    true,
		},
		{
			name:    "just confab save",
			command: "confab save",
			want:    true,
		},
		{
			name:    "confab without args",
			command: "confab",
			want:    true,
		},
		{
			name:    "path with confab",
			command: "/home/user/.local/bin/confab",
			want:    true,
		},
		{
			name:    "not confab - different name",
			command: "/usr/bin/notconfab save",
			want:    false,
		},
		{
			name:    "not confab - confab in path but not executable",
			command: "/home/confab/bin/other-tool save",
			want:    false,
		},
		{
			name:    "empty command",
			command: "",
			want:    false,
		},
		{
			name:    "confab as substring",
			command: "myconfab save",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConfabCommand(tt.command)
			if got != tt.want {
				t.Errorf("isConfabCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}
func TestIsConfabHookEntry(t *testing.T) {
	tests := []struct {
		name string
		hook map[string]any
		want bool
	}{
		{
			name: "confab command",
			hook: map[string]any{"type": "command", "command": "/usr/bin/confab hook session-start"},
			want: true,
		},
		{
			name: "non-confab command",
			hook: map[string]any{"type": "command", "command": "/usr/bin/other-tool run"},
			want: false,
		},
		{
			name: "missing type",
			hook: map[string]any{"command": "/usr/bin/confab save"},
			want: false,
		},
		{
			name: "missing command",
			hook: map[string]any{"type": "command"},
			want: false,
		},
		{
			name: "non-command type",
			hook: map[string]any{"type": "url", "command": "/usr/bin/confab save"},
			want: false,
		},
		{
			name: "empty map",
			hook: map[string]any{},
			want: false,
		},
		{
			name: "command is not a string",
			hook: map[string]any{"type": "command", "command": 42},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConfabHookEntry(tt.hook)
			if got != tt.want {
				t.Errorf("isConfabHookEntry() = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestClaudeHookSettingsJSONShape(t *testing.T) {
	settings := config.NewClaudeSettings()

	mustInstall := func(hook map[string]any, event, matcher string, hasMatcher bool) {
		t.Helper()
		if err := installHook(settings, hook, event, matcher, hasMatcher); err != nil {
			t.Fatalf("installHook(%s) error = %v", event, err)
		}
	}

	mustInstall(map[string]any{"type": "command", "command": "/usr/bin/confab hook session-start"}, "SessionStart", "*", true)
	mustInstall(map[string]any{"type": "command", "command": "/usr/bin/confab hook session-end"}, "SessionEnd", "*", true)
	for _, matcher := range toolUseMatchers {
		mustInstall(map[string]any{"type": "command", "command": "/usr/bin/confab hook pre-tool-use"}, "PreToolUse", matcher, true)
		mustInstall(map[string]any{"type": "command", "command": "/usr/bin/confab hook post-tool-use"}, "PostToolUse", matcher, true)
	}
	mustInstall(map[string]any{"type": "command", "command": "/usr/bin/confab hook user-prompt-submit"}, "UserPromptSubmit", "", false)

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal settings: %v", err)
	}

	want := `{
  "hooks": {
    "PostToolUse": [
      {
        "hooks": [
          {
            "command": "/usr/bin/confab hook post-tool-use",
            "type": "command"
          }
        ],
        "matcher": "Bash"
      },
      {
        "hooks": [
          {
            "command": "/usr/bin/confab hook post-tool-use",
            "type": "command"
          }
        ],
        "matcher": "mcp__github__create_pull_request"
      }
    ],
    "PreToolUse": [
      {
        "hooks": [
          {
            "command": "/usr/bin/confab hook pre-tool-use",
            "type": "command"
          }
        ],
        "matcher": "Bash"
      },
      {
        "hooks": [
          {
            "command": "/usr/bin/confab hook pre-tool-use",
            "type": "command"
          }
        ],
        "matcher": "mcp__github__create_pull_request"
      }
    ],
    "SessionEnd": [
      {
        "hooks": [
          {
            "command": "/usr/bin/confab hook session-end",
            "type": "command"
          }
        ],
        "matcher": "*"
      }
    ],
    "SessionStart": [
      {
        "hooks": [
          {
            "command": "/usr/bin/confab hook session-start",
            "type": "command"
          }
        ],
        "matcher": "*"
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "command": "/usr/bin/confab hook user-prompt-submit",
            "type": "command"
          }
        ]
      }
    ]
  }
}`
	if string(data) != want {
		t.Fatalf("settings JSON changed:\n%s", string(data))
	}
}
func TestGetHooksList(t *testing.T) {
	tests := []struct {
		name      string
		entry     map[string]any
		wantNil   bool
		wantCount int
	}{
		{
			name:      "valid hooks array",
			entry:     map[string]any{"hooks": []any{map[string]any{"type": "command"}}},
			wantNil:   false,
			wantCount: 1,
		},
		{
			name:      "empty hooks array",
			entry:     map[string]any{"hooks": []any{}},
			wantNil:   false,
			wantCount: 0,
		},
		{
			name:    "missing hooks key",
			entry:   map[string]any{"matcher": "*"},
			wantNil: true,
		},
		{
			name:    "hooks is wrong type (string)",
			entry:   map[string]any{"hooks": "not an array"},
			wantNil: true,
		},
		{
			name:    "hooks is wrong type (int)",
			entry:   map[string]any{"hooks": 42},
			wantNil: true,
		},
		{
			name:    "empty entry",
			entry:   map[string]any{},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getHooksList(tt.entry, "TestEvent", 0)
			if tt.wantNil {
				if got != nil {
					t.Errorf("getHooksList() = %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Fatal("getHooksList() = nil, want non-nil")
				}
				if len(got) != tt.wantCount {
					t.Errorf("getHooksList() returned %d items, want %d", len(got), tt.wantCount)
				}
			}
		})
	}
}
func TestInstallHook_WithMatcher(t *testing.T) {
	confabHook := map[string]any{"type": "command", "command": "/usr/bin/confab hook session-start"}

	t.Run("creates new entry when no matcher exists", func(t *testing.T) {
		settings := config.NewClaudeSettings()

		if err := installHook(settings, confabHook, "SessionStart", "*", true); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if len(eventHooks) != 1 {
			t.Fatalf("expected 1 matcher, got %d", len(eventHooks))
		}
		entry := eventHooks[0].(map[string]any)
		if entry["matcher"] != "*" {
			t.Errorf("expected matcher '*', got %v", entry["matcher"])
		}
		hooks := entry["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("expected 1 hook, got %d", len(hooks))
		}
		hook := hooks[0].(map[string]any)
		if hook["command"] != "/usr/bin/confab hook session-start" {
			t.Errorf("unexpected command: %v", hook["command"])
		}
	})

	t.Run("updates existing confab hook in place", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		oldHook := makeHook("command", "/old/confab save")
		setTestHook(settings, "SessionStart", makeMatcher("*", oldHook))

		if err := installHook(settings, confabHook, "SessionStart", "*", true); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if len(eventHooks) != 1 {
			t.Fatalf("expected 1 matcher, got %d", len(eventHooks))
		}
		hooks := eventHooks[0].(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("expected 1 hook (update in place), got %d", len(hooks))
		}
		hook := hooks[0].(map[string]any)
		if hook["command"] != "/usr/bin/confab hook session-start" {
			t.Errorf("hook was not updated: %v", hook["command"])
		}
	})

	t.Run("appends to existing matcher with other hooks", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		otherHook := makeHook("command", "/usr/bin/other-tool run")
		setTestHook(settings, "SessionStart", makeMatcher("*", otherHook))

		if err := installHook(settings, confabHook, "SessionStart", "*", true); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		hooks := settings.GetEventHooks("SessionStart")[0].(map[string]any)["hooks"].([]any)
		if len(hooks) != 2 {
			t.Fatalf("expected 2 hooks, got %d", len(hooks))
		}
	})

	t.Run("does not match different matcher value", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		setTestHook(settings, "PreToolUse", makeMatcher("Write"))

		if err := installHook(settings, confabHook, "PreToolUse", "Bash", true); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("PreToolUse")
		if len(eventHooks) != 2 {
			t.Fatalf("expected 2 matchers (Write + new Bash), got %d", len(eventHooks))
		}
	})

	t.Run("skips malformed entries", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		// Set up an event with a non-map entry followed by a valid matcher
		if err := settings.SetEventHooks("SessionStart", []any{
			"not a map",
			map[string]any{"matcher": "*", "hooks": []any{}},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if err := installHook(settings, confabHook, "SessionStart", "*", true); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		// The malformed entry is skipped, the valid "*" matcher is found and used
		if len(eventHooks) != 2 {
			t.Fatalf("expected 2 entries (malformed + valid), got %d", len(eventHooks))
		}
		// Hook should be in the second entry (the valid matcher)
		hooks := eventHooks[1].(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("expected 1 hook in valid matcher, got %d", len(hooks))
		}
	})

	t.Run("skips entry with matcher null", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		// Entry with matcher: null should not match hasMatcher=true with matcherValue="*"
		if err := settings.SetEventHooks("SessionStart", []any{
			map[string]any{"matcher": nil, "hooks": []any{}},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if err := installHook(settings, confabHook, "SessionStart", "*", true); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if len(eventHooks) != 2 {
			t.Fatalf("expected 2 entries (null matcher + new *), got %d", len(eventHooks))
		}
	})
}
func TestInstallHook_WithoutMatcher(t *testing.T) {
	confabHook := map[string]any{"type": "command", "command": "/usr/bin/confab hook user-prompt-submit"}

	t.Run("creates new entry without matcher key", func(t *testing.T) {
		settings := config.NewClaudeSettings()

		if err := installHook(settings, confabHook, "UserPromptSubmit", "", false); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("UserPromptSubmit")
		if len(eventHooks) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(eventHooks))
		}
		entry := eventHooks[0].(map[string]any)
		if _, has := entry["matcher"]; has {
			t.Error("expected no matcher key, but found one")
		}
		hooks := entry["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("expected 1 hook, got %d", len(hooks))
		}
	})

	t.Run("updates existing confab hook in matcherless entry", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		oldHook := makeHook("command", "/old/confab hook user-prompt-submit")
		// Create an entry without a matcher key
		hooksList := []any{map[string]any(oldHook)}
		if err := settings.SetEventHooks("UserPromptSubmit", []any{
			map[string]any{"hooks": hooksList},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if err := installHook(settings, confabHook, "UserPromptSubmit", "", false); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		hooks := settings.GetEventHooks("UserPromptSubmit")[0].(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("expected 1 hook (update in place), got %d", len(hooks))
		}
		hook := hooks[0].(map[string]any)
		if hook["command"] != "/usr/bin/confab hook user-prompt-submit" {
			t.Errorf("hook was not updated: %v", hook["command"])
		}
	})

	t.Run("skips entries with matcher key", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		// Only an entry with a matcher exists
		setTestHook(settings, "UserPromptSubmit", makeMatcher("*"))

		if err := installHook(settings, confabHook, "UserPromptSubmit", "", false); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("UserPromptSubmit")
		if len(eventHooks) != 2 {
			t.Fatalf("expected 2 entries (matcher + new matcherless), got %d", len(eventHooks))
		}
	})

	t.Run("skips entry with matcher null (key present)", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		// matcher: null means key IS present, so hasMatcher=false should skip it
		if err := settings.SetEventHooks("UserPromptSubmit", []any{
			map[string]any{"matcher": nil, "hooks": []any{}},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if err := installHook(settings, confabHook, "UserPromptSubmit", "", false); err != nil {
			t.Fatalf("installHook failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("UserPromptSubmit")
		if len(eventHooks) != 2 {
			t.Fatalf("expected 2 entries (null matcher + new matcherless), got %d", len(eventHooks))
		}
	})
}
func TestRemoveHooksFromEvent(t *testing.T) {
	t.Run("removes all confab hooks", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		confabHook := makeHook("command", "/usr/bin/confab hook session-start")
		setTestHook(settings, "SessionStart", makeMatcher("*", confabHook))

		if err := removeHooksFromEvent(settings, "SessionStart", isConfabHookEntry); err != nil {
			t.Fatalf("removeHooksFromEvent failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if len(eventHooks) != 0 {
			t.Errorf("expected 0 matchers after removing only hook, got %d", len(eventHooks))
		}
	})

	t.Run("preserves non-confab hooks", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		confabHook := makeHook("command", "/usr/bin/confab save")
		otherHook := makeHook("command", "/usr/bin/other-tool run")
		setTestHook(settings, "SessionStart", makeMatcher("*", confabHook, otherHook))

		if err := removeHooksFromEvent(settings, "SessionStart", isConfabHookEntry); err != nil {
			t.Fatalf("removeHooksFromEvent failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if len(eventHooks) != 1 {
			t.Fatalf("expected 1 matcher remaining, got %d", len(eventHooks))
		}
		hooks := eventHooks[0].(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("expected 1 hook remaining, got %d", len(hooks))
		}
		cmd := hooks[0].(map[string]any)["command"].(string)
		if cmd != "/usr/bin/other-tool run" {
			t.Errorf("wrong hook preserved: %s", cmd)
		}
	})

	t.Run("custom predicate", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		hook1 := makeHook("command", "/usr/bin/confab sync start")
		hook2 := makeHook("command", "/usr/bin/confab hook session-start")
		setTestHook(settings, "SessionStart", makeMatcher("*", hook1, hook2))

		// Remove only hooks containing "sync start"
		err := removeHooksFromEvent(settings, "SessionStart", func(hook map[string]any) bool {
			cmd, _ := hook["command"].(string)
			return strings.Contains(cmd, "sync start")
		})
		if err != nil {
			t.Fatalf("removeHooksFromEvent failed: %v", err)
		}

		hooks := settings.GetEventHooks("SessionStart")[0].(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("expected 1 hook remaining, got %d", len(hooks))
		}
		cmd := hooks[0].(map[string]any)["command"].(string)
		if cmd != "/usr/bin/confab hook session-start" {
			t.Errorf("wrong hook preserved: %s", cmd)
		}
	})

	t.Run("drops empty matchers", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		confabHook := makeHook("command", "/usr/bin/confab save")
		otherHook := makeHook("command", "/usr/bin/other-tool run")
		// Two matchers: first has only confab (will be dropped), second has other (will remain)
		if err := settings.SetEventHooks("SessionStart", []any{
			map[string]any{"matcher": "*", "hooks": []any{map[string]any(confabHook)}},
			map[string]any{"matcher": "Bash", "hooks": []any{map[string]any(otherHook)}},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if err := removeHooksFromEvent(settings, "SessionStart", isConfabHookEntry); err != nil {
			t.Fatalf("removeHooksFromEvent failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if len(eventHooks) != 1 {
			t.Fatalf("expected 1 matcher remaining, got %d", len(eventHooks))
		}
		if eventHooks[0].(map[string]any)["matcher"] != "Bash" {
			t.Error("wrong matcher preserved")
		}
	})

	t.Run("preserves malformed entries", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		if err := settings.SetEventHooks("SessionStart", []any{
			"not a map",
			map[string]any{"matcher": "*", "hooks": []any{
				map[string]any{"type": "command", "command": "/usr/bin/confab save"},
			}},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if err := removeHooksFromEvent(settings, "SessionStart", isConfabHookEntry); err != nil {
			t.Fatalf("removeHooksFromEvent failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		// malformed entry preserved, empty matcher dropped
		if len(eventHooks) != 1 {
			t.Fatalf("expected 1 entry (malformed preserved), got %d", len(eventHooks))
		}
	})

	t.Run("preserves matcher with missing hooks key", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		if err := settings.SetEventHooks("SessionStart", []any{
			map[string]any{"matcher": "*"},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if err := removeHooksFromEvent(settings, "SessionStart", isConfabHookEntry); err != nil {
			t.Fatalf("removeHooksFromEvent failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if len(eventHooks) != 1 {
			t.Fatalf("expected 1 matcher preserved, got %d", len(eventHooks))
		}
	})

	t.Run("preserves non-map hook entries", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		if err := settings.SetEventHooks("SessionStart", []any{
			map[string]any{"matcher": "*", "hooks": []any{
				"not a map",
				map[string]any{"type": "command", "command": "/usr/bin/confab save"},
			}},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if err := removeHooksFromEvent(settings, "SessionStart", isConfabHookEntry); err != nil {
			t.Fatalf("removeHooksFromEvent failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if len(eventHooks) != 1 {
			t.Fatalf("expected 1 matcher, got %d", len(eventHooks))
		}
		hooks := eventHooks[0].(map[string]any)["hooks"].([]any)
		if len(hooks) != 1 {
			t.Fatalf("expected 1 hook (non-map preserved), got %d", len(hooks))
		}
	})

	t.Run("no-op on empty event", func(t *testing.T) {
		settings := config.NewClaudeSettings()

		if err := removeHooksFromEvent(settings, "SessionStart", isConfabHookEntry); err != nil {
			t.Fatalf("removeHooksFromEvent failed: %v", err)
		}

		eventHooks := settings.GetEventHooks("SessionStart")
		if eventHooks != nil {
			t.Errorf("expected nil, got %v", eventHooks)
		}
	})
}
func TestFindHookInEvent(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		setTestHook(settings, "SessionStart", makeMatcher("*", makeHook("command", "/usr/bin/confab save")))

		if !findHookInEvent(settings, "SessionStart", isConfabHookEntry) {
			t.Error("expected to find confab hook")
		}
	})

	t.Run("not found", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		setTestHook(settings, "SessionStart", makeMatcher("*", makeHook("command", "/usr/bin/other-tool")))

		if findHookInEvent(settings, "SessionStart", isConfabHookEntry) {
			t.Error("expected not to find confab hook")
		}
	})

	t.Run("empty event", func(t *testing.T) {
		settings := config.NewClaudeSettings()

		if findHookInEvent(settings, "SessionStart", isConfabHookEntry) {
			t.Error("expected false for empty event")
		}
	})

	t.Run("skips malformed entries", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		if err := settings.SetEventHooks("SessionStart", []any{
			"not a map",
			map[string]any{"matcher": "*", "hooks": []any{
				"also not a map",
				map[string]any{"type": "command", "command": "/usr/bin/confab save"},
			}},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if !findHookInEvent(settings, "SessionStart", isConfabHookEntry) {
			t.Error("expected to find confab hook despite malformed entries")
		}
	})

	t.Run("searches across multiple matchers", func(t *testing.T) {
		settings := config.NewClaudeSettings()
		if err := settings.SetEventHooks("PreToolUse", []any{
			map[string]any{"matcher": "Write", "hooks": []any{
				map[string]any{"type": "command", "command": "/usr/bin/other-tool"},
			}},
			map[string]any{"matcher": "Bash", "hooks": []any{
				map[string]any{"type": "command", "command": "/usr/bin/confab hook pre-tool-use"},
			}},
		}); err != nil {
			t.Fatalf("setEventHooks failed: %v", err)
		}

		if !findHookInEvent(settings, "PreToolUse", isConfabHookEntry) {
			t.Error("expected to find confab hook in second matcher")
		}
	})
}
func hasHookWithCommandSubstring(settings *config.ClaudeSettings, eventName, substr string) bool {
	return findHookInEvent(settings, eventName, func(hook map[string]any) bool {
		cmd, _ := hook["command"].(string)
		return hook["type"] == "command" && strings.Contains(cmd, substr)
	})
}
func TestIsSyncHooksInstalled(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(config.ClaudeStateDirEnv)
	os.Setenv(config.ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(config.ClaudeStateDirEnv, oldEnv)

	os.MkdirAll(tmpDir, 0755)

	// Initially not installed
	settings, err := config.ReadSettings()
	if err != nil {
		t.Fatalf("config.ReadSettings failed: %v", err)
	}
	if hasSyncHooks(settings) {
		t.Error("Expected sync hooks to not be installed initially")
	}

	// Install sync hooks
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Now should be installed
	settings, err = config.ReadSettings()
	if err != nil {
		t.Fatalf("config.ReadSettings failed: %v", err)
	}
	if !hasSyncHooks(settings) {
		t.Error("Expected sync hooks to be installed after InstallSyncHooks")
	}
}
func hasSyncHooks(settings *config.ClaudeSettings) bool {
	hasStart := hasHookWithCommandSubstring(settings, "SessionStart", "hook session-start")
	hasEnd := hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end")
	return hasStart && hasEnd
}

// TestIsPreToolUseHooksInstalled / TestIsPostToolUseHooksInstalled /
// TestIsUserPromptSubmitHookInstalled bracket the three "Is*Installed"
// checkers (claude.go:266, 303, 336) that gate daemon-side hook
// verification. Without these, a regression in any of the three could
// silently report "hooks missing" and trigger reinstall prompts on every
// CLI invocation.
//
// Pre-populates settings.json with `/usr/local/bin/confab` commands so
// isConfabCommand's basename check passes — the test binary is named
// `hookconfig.test`, not `confab`, so install-then-read round-trips
// would otherwise read back hooks the production check rejects.
func TestIsPreToolUseHooksInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClaudeStateDirEnv, tmpDir)

	// Initially no settings file → not installed.
	if installed, err := IsPreToolUseHooksInstalled(); err != nil {
		t.Fatalf("IsPreToolUseHooksInstalled initial: %v", err)
	} else if installed {
		t.Error("expected PreToolUse hooks to not be installed initially")
	}

	const installed = `{
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook pre-tool-use"}]}]
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(installed), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if ok, err := IsPreToolUseHooksInstalled(); err != nil {
		t.Fatalf("IsPreToolUseHooksInstalled after install: %v", err)
	} else if !ok {
		t.Error("IsPreToolUseHooksInstalled() = false after manual install; want true")
	}

	if err := UninstallPreToolUseHooks(); err != nil {
		t.Fatalf("UninstallPreToolUseHooks failed: %v", err)
	}
	if ok, err := IsPreToolUseHooksInstalled(); err != nil {
		t.Fatalf("IsPreToolUseHooksInstalled after uninstall: %v", err)
	} else if ok {
		t.Error("IsPreToolUseHooksInstalled() = true after uninstall; want false")
	}
}

func TestIsPostToolUseHooksInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClaudeStateDirEnv, tmpDir)

	if installed, err := IsPostToolUseHooksInstalled(); err != nil {
		t.Fatalf("IsPostToolUseHooksInstalled initial: %v", err)
	} else if installed {
		t.Error("expected PostToolUse hooks to not be installed initially")
	}

	const installed = `{
  "hooks": {
    "PostToolUse": [{"matcher": "Bash", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook post-tool-use"}]}]
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(installed), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if ok, err := IsPostToolUseHooksInstalled(); err != nil {
		t.Fatalf("IsPostToolUseHooksInstalled after install: %v", err)
	} else if !ok {
		t.Error("IsPostToolUseHooksInstalled() = false after manual install; want true")
	}

	if err := UninstallPostToolUseHooks(); err != nil {
		t.Fatalf("UninstallPostToolUseHooks failed: %v", err)
	}
	if ok, err := IsPostToolUseHooksInstalled(); err != nil {
		t.Fatalf("IsPostToolUseHooksInstalled after uninstall: %v", err)
	} else if ok {
		t.Error("IsPostToolUseHooksInstalled() = true after uninstall; want false")
	}
}

func TestIsUserPromptSubmitHookInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClaudeStateDirEnv, tmpDir)

	if installed, err := IsUserPromptSubmitHookInstalled(); err != nil {
		t.Fatalf("IsUserPromptSubmitHookInstalled initial: %v", err)
	} else if installed {
		t.Error("expected UserPromptSubmit hook to not be installed initially")
	}

	const installed = `{
  "hooks": {
    "UserPromptSubmit": [{"hooks": [{"type":"command","command":"/usr/local/bin/confab hook user-prompt-submit"}]}]
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(installed), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if ok, err := IsUserPromptSubmitHookInstalled(); err != nil {
		t.Fatalf("IsUserPromptSubmitHookInstalled after install: %v", err)
	} else if !ok {
		t.Error("IsUserPromptSubmitHookInstalled() = false after manual install; want true")
	}

	if err := UninstallUserPromptSubmitHook(); err != nil {
		t.Fatalf("UninstallUserPromptSubmitHook failed: %v", err)
	}
	if ok, err := IsUserPromptSubmitHookInstalled(); err != nil {
		t.Fatalf("IsUserPromptSubmitHookInstalled after uninstall: %v", err)
	} else if ok {
		t.Error("IsUserPromptSubmitHookInstalled() = true after uninstall; want false")
	}
}
func TestUninstallSyncHooks(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(config.ClaudeStateDirEnv)
	os.Setenv(config.ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(config.ClaudeStateDirEnv, oldEnv)

	os.MkdirAll(tmpDir, 0755)

	// Install sync hooks first
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Verify installed
	settings, _ := config.ReadSettings()
	if !hasSyncHooks(settings) {
		t.Fatal("Sync hooks should be installed before testing uninstall")
	}

	// Uninstall
	if err := UninstallSyncHooks(); err != nil {
		t.Fatalf("UninstallSyncHooks failed: %v", err)
	}

	// Verify uninstalled
	settings, err := config.ReadSettings()
	if err != nil {
		t.Fatalf("config.ReadSettings failed: %v", err)
	}

	if hasHookWithCommandSubstring(settings, "SessionStart", "hook session-start") {
		t.Error("Found 'hook session-start' hook in SessionStart after uninstall")
	}
	if hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end") {
		t.Error("Found 'hook session-end' hook in SessionEnd after uninstall")
	}
}
func TestInstallSyncHooks_PreservesOtherHooks(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(config.ClaudeStateDirEnv)
	os.Setenv(config.ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(config.ClaudeStateDirEnv, oldEnv)

	os.MkdirAll(tmpDir, 0755)

	// Install some other hook first
	err := config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		setTestHook(settings, "SessionEnd",
			makeMatcher("*", makeHook("command", "/usr/bin/other-tool log")),
		)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to install other hook: %v", err)
	}

	// Install sync hooks
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Verify other hook is preserved
	settings, err := config.ReadSettings()
	if err != nil {
		t.Fatalf("config.ReadSettings failed: %v", err)
	}

	foundOther := hasHookWithCommandSubstring(settings, "SessionEnd", "/usr/bin/other-tool log")
	foundSessionEnd := hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end")

	if !foundOther {
		t.Error("Other hook was not preserved after InstallSyncHooks")
	}
	if !foundSessionEnd {
		t.Error("Session-end hook was not installed")
	}
}
func TestInstallSyncHooks_UpdatesExistingConfab(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(config.ClaudeStateDirEnv)
	os.Setenv(config.ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(config.ClaudeStateDirEnv, oldEnv)

	os.MkdirAll(tmpDir, 0755)

	// Install old-style save hook (simulating existing confab installation)
	err := config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		setTestHook(settings, "SessionEnd",
			makeMatcher("*", makeHook("command", "/old/path/confab save")),
		)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to install old hook: %v", err)
	}

	// Install sync hooks (should update the existing confab hook)
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Verify the hook was updated to session-end
	settings, err := config.ReadSettings()
	if err != nil {
		t.Fatalf("config.ReadSettings failed: %v", err)
	}

	foundSessionEnd := hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end")
	foundOldSave := hasHookWithCommandSubstring(settings, "SessionEnd", "/old/path/confab save")

	if !foundSessionEnd {
		t.Error("Expected session-end hook to be installed")
	}
	if foundOldSave {
		t.Error("Old save hook should have been replaced")
	}
}
func TestAtomicUpdateSettings_PreservesUnknownHookFields(t *testing.T) {
	// This test ensures that unknown fields within hooks are preserved.
	// The hooks schema is controlled by Claude Code and evolves rapidly,
	// so we must not drop any fields we don't recognize.

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(config.ClaudeStateDirEnv)
	os.Setenv(config.ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(config.ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Write a settings file with hooks that have extra/unknown fields
	initialSettings := `{
  "hooks": {
    "SessionEnd": [
      {
        "matcher": "*",
        "unknownMatcherField": "should-be-preserved",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/bin/other-tool",
            "timeout": 5000,
            "environment": {"FOO": "bar"},
            "unknownHookField": "also-preserved"
          }
        ]
      }
    ]
  }
}`

	if err := os.WriteFile(settingsPath, []byte(initialSettings), 0644); err != nil {
		t.Fatalf("Failed to write initial settings: %v", err)
	}

	// Install sync hooks
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Read back and verify unknown fields are preserved
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	hooks := raw["hooks"].(map[string]any)
	sessionEnd := hooks["SessionEnd"].([]any)

	// Find the matcher with the other-tool hook
	for _, matcherAny := range sessionEnd {
		matcher := matcherAny.(map[string]any)

		// Check unknown matcher field is preserved
		if matcher["unknownMatcherField"] == "should-be-preserved" {
			// Found the original matcher, check hook fields
			hooksList := matcher["hooks"].([]any)
			for _, hookAny := range hooksList {
				hook := hookAny.(map[string]any)
				cmd, _ := hook["command"].(string)
				if strings.Contains(cmd, "other-tool") {
					// Check unknown fields
					if hook["timeout"] != float64(5000) {
						t.Errorf("timeout field lost or changed: %v", hook["timeout"])
					}
					if hook["unknownHookField"] != "also-preserved" {
						t.Errorf("unknownHookField lost or changed: %v", hook["unknownHookField"])
					}
					env, ok := hook["environment"].(map[string]any)
					if !ok || env["FOO"] != "bar" {
						t.Errorf("environment field lost or changed: %v", hook["environment"])
					}
				}
			}
		}
	}
}
func TestUninstallHooks_CleansUpEmptySections(t *testing.T) {
	// This test ensures that when all hooks are removed from an event,
	// the event key is removed entirely (not left as null or empty array).
	// Additionally, if all events are removed, the "hooks" key should be removed.

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(config.ClaudeStateDirEnv)
	os.Setenv(config.ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(config.ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Install hooks
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}
	if err := InstallPreToolUseHooks(); err != nil {
		t.Fatalf("InstallPreToolUseHooks failed: %v", err)
	}

	// Verify hooks are installed
	settings, err := config.ReadSettings()
	if err != nil {
		t.Fatalf("config.ReadSettings failed: %v", err)
	}
	hooksMap, _ := settings.GetHooksMap()
	if len(hooksMap) == 0 {
		t.Fatal("Expected hooks to be installed")
	}

	// Uninstall all hooks
	if err := UninstallSyncHooks(); err != nil {
		t.Fatalf("UninstallSyncHooks failed: %v", err)
	}
	if err := UninstallPreToolUseHooks(); err != nil {
		t.Fatalf("UninstallPreToolUseHooks failed: %v", err)
	}

	// Read the raw JSON to check for null/empty values
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	// The "hooks" key should be removed entirely when empty
	if hooksRaw, exists := raw["hooks"]; exists {
		// If hooks exists, it should not be empty or contain only empty/null values
		if hooks, ok := hooksRaw.(map[string]any); ok {
			for eventName, eventHooks := range hooks {
				if eventHooks == nil {
					t.Errorf("Event %q has null value - should be removed entirely", eventName)
				}
				if arr, ok := eventHooks.([]any); ok && len(arr) == 0 {
					t.Errorf("Event %q has empty array - should be removed entirely", eventName)
				}
			}
			if len(hooks) == 0 {
				t.Error("hooks map is empty - should be removed entirely from settings")
			}
		}
	}
	// If "hooks" doesn't exist, that's the correct behavior
}
func TestUninstallHooks_FromCleanSettings(t *testing.T) {
	// When uninstalling from settings that have no hooks,
	// we should not leave an empty "hooks": {} behind

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(config.ClaudeStateDirEnv)
	os.Setenv(config.ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(config.ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Create settings with no hooks
	initialSettings := `{
  "someOtherSetting": "value"
}`
	if err := os.WriteFile(settingsPath, []byte(initialSettings), 0644); err != nil {
		t.Fatalf("Failed to write initial settings: %v", err)
	}

	// Uninstall hooks (even though none exist)
	if err := UninstallSyncHooks(); err != nil {
		t.Fatalf("UninstallSyncHooks failed: %v", err)
	}
	if err := UninstallPreToolUseHooks(); err != nil {
		t.Fatalf("UninstallPreToolUseHooks failed: %v", err)
	}

	// Read back and verify no empty hooks object was created
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	// Should not have a "hooks" key at all
	if _, exists := raw["hooks"]; exists {
		t.Errorf("Empty hooks object was created - should not exist. Got: %v", raw["hooks"])
	}

	// Other settings should be preserved
	if raw["someOtherSetting"] != "value" {
		t.Errorf("Other settings were not preserved: %v", raw)
	}
}
func TestUninstallHooks_PreservesOtherHooksInSameEvent(t *testing.T) {
	// When removing confab hooks, other hooks in the same event should remain
	// and the event key should NOT be removed

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(config.ClaudeStateDirEnv)
	os.Setenv(config.ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(config.ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Create settings with both confab and other hooks
	initialSettings := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "/path/to/confab hook pre-tool-use"},
          {"type": "command", "command": "/other/tool check"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(initialSettings), 0644); err != nil {
		t.Fatalf("Failed to write initial settings: %v", err)
	}

	// Uninstall confab hooks
	if err := UninstallPreToolUseHooks(); err != nil {
		t.Fatalf("UninstallPreToolUseHooks failed: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	// hooks and PreToolUse should still exist
	hooks, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key should still exist when other hooks remain")
	}

	preToolUse, ok := hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatal("PreToolUse key should still exist when other hooks remain")
	}

	// Should have exactly one matcher with one hook
	if len(preToolUse) != 1 {
		t.Fatalf("Expected 1 matcher, got %d", len(preToolUse))
	}

	matcher := preToolUse[0].(map[string]any)
	hooksList := matcher["hooks"].([]any)
	if len(hooksList) != 1 {
		t.Fatalf("Expected 1 hook remaining, got %d", len(hooksList))
	}

	hook := hooksList[0].(map[string]any)
	if hook["command"] != "/other/tool check" {
		t.Errorf("Wrong hook remaining: %v", hook["command"])
	}
}
