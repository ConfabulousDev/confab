package hookconfig

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// These tests cover the ownership-based Codex config editor (kata cd4q):
// the pure text transform, the pre-write verification, and the file-level
// write path. Fixtures are synthetic; none is a copy of a real config.

const (
	testCodexPath = "/Users/test/.codex/config.toml"
	testCodexBin  = "/usr/local/bin/confab"

	// codexTestHead is user-owned config that sits above confab's hooks.
	codexTestHead = `model = "gpt-5"
approval_policy = "on-request"

[projects."/Users/test/dev/app"]
trust_level = "trusted"

[tui]
theme = "dark"

[mcp_servers.linear]
command = "npx"
args = ["-y", "mcp-remote"]

[features]
hooks = true
`

	// codexTestTail is what Codex's own config writer adds after confab's
	// last table and before the trailing end-marker comment.
	codexTestTail = `[desktop]
sidebar = true

[marketplaces.openai-bundled]
source = "bundled"

[plugins."browser@openai-bundled"]
enabled = true
`

	codexTestUserGroup = `[[hooks.SessionStart]]
matcher = "startup"
[[hooks.SessionStart.hooks]]
type = "command"
command = "/usr/bin/other start"
`
)

var codexTestEvents = []string{"SessionStart", "PreToolUse", "PostToolUse"}

// codexTestPieces splits the canonical block into its three hook groups and
// three trust-state tables, so fixtures follow whatever codexHooksTOML emits.
type codexTestPieces struct {
	groups [3]string
	states [3]string
}

func codexPieces(t *testing.T, configPath string, idx codexHookGroupIndices) codexTestPieces {
	t.Helper()
	chunks := strings.Split(strings.TrimRight(codexHooksTOML(configPath, testCodexBin, idx), "\n"), "\n\n")
	if len(chunks) != 6 {
		t.Fatalf("canonical block has %d blank-line separated chunks, want 6", len(chunks))
	}
	var p codexTestPieces
	for i := 0; i < 3; i++ {
		p.groups[i] = chunks[i] + "\n"
		p.states[i] = chunks[i+3] + "\n"
	}
	return p
}

func codexTestBlock() string {
	return codexHooksTOML(testCodexPath, testCodexBin, codexHookGroupIndices{})
}

// codexTestOldBlock is the block as written before the product rename
// (kata y7wh): old status message with its own matching hash.
func codexTestOldBlock(t *testing.T) string {
	t.Helper()
	const oldMsg = "Starting Confab sync"
	cmd := testCodexBin + " hook session-start --provider codex"
	block := codexTestBlock()
	matcher := codexSessionStartMatcherOf(t, block)
	old := strings.ReplaceAll(block, codexSessionStartStatusMessage, oldMsg)
	old = strings.ReplaceAll(old,
		codexTrustedHookHash("session_start", matcher, cmd, codexSessionStartStatusMessage),
		codexTrustedHookHash("session_start", matcher, cmd, oldMsg))
	if old == block || !strings.Contains(old, oldMsg) {
		t.Fatalf("fixture did not produce a pre-rename block\n%s", old)
	}
	return old
}

func codexSessionStartMatcherOf(t *testing.T, block string) string {
	t.Helper()
	doc := mustParseTOML(t, block)
	groups := doc["hooks"].(map[string]any)["SessionStart"].([]any)
	return groups[0].(map[string]any)["matcher"].(string)
}

// realShapedCodexConfig mirrors the broken shape seen in the field: an
// outdated confab block between markers, Codex-owned tables between confab's
// last table and the end marker, end marker last with no trailing newline.
func realShapedCodexConfig(t *testing.T) string {
	t.Helper()
	return codexTestHead + "\n" + confabCodexHooksStart + "\n" + codexTestOldBlock(t) + "\n" + codexTestTail + confabCodexHooksEnd
}

func mustParseTOML(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := toml.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("not valid TOML: %v\n%s", err, s)
	}
	return doc
}

func mustTransform(t *testing.T, input string, mode codexEditMode) (string, []string) {
	t.Helper()
	out, notices, err := transformCodexConfig(input, testCodexPath, testCodexBin, mode)
	if err != nil {
		t.Fatalf("transformCodexConfig() error = %v\ninput:\n%s", err, input)
	}
	if err := verifyCodexEdit(input, out, testCodexPath, testCodexBin, mode); err != nil {
		t.Fatalf("transform output fails verification: %v\noutput:\n%s", err, out)
	}
	return out, notices
}

// confabHandlerCount counts confab command handlers for one event in the
// parsed document.
func confabHandlerCount(t *testing.T, s, event string) int {
	t.Helper()
	hooks, _ := mustParseTOML(t, s)["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	n := 0
	for _, g := range groups {
		handlers, _ := g.(map[string]any)["hooks"].([]any)
		for _, h := range handlers {
			if cmd, _ := h.(map[string]any)["command"].(string); isConfabCommand(cmd) {
				n++
			}
		}
	}
	return n
}

func assertOneConfabHookSet(t *testing.T, s string) {
	t.Helper()
	for _, event := range codexTestEvents {
		if n := confabHandlerCount(t, s, event); n != 1 {
			t.Errorf("%s has %d confab handlers, want exactly 1\n%s", event, n, s)
		}
	}
}

func assertNoConfabHooks(t *testing.T, s string) {
	t.Helper()
	for _, event := range codexTestEvents {
		if n := confabHandlerCount(t, s, event); n != 0 {
			t.Errorf("%s still has %d confab handlers after uninstall\n%s", event, n, s)
		}
	}
}

func assertNoMarkers(t *testing.T, s string) {
	t.Helper()
	for _, marker := range []string{confabCodexHooksStart, confabCodexHooksEnd} {
		if strings.Contains(s, marker) {
			t.Errorf("output still contains marker %q\n%s", marker, s)
		}
	}
}

func assertNotice(t *testing.T, notices []string, wants ...string) {
	t.Helper()
	for _, n := range notices {
		ok := true
		for _, w := range wants {
			if !strings.Contains(n, w) {
				ok = false
			}
		}
		if ok {
			return
		}
	}
	t.Errorf("no notice containing all of %q; got %q", wants, notices)
}

func TestCodexTransformKeepsTablesBetweenMarkers(t *testing.T) {
	input := realShapedCodexConfig(t)
	current := codexTestHead + "\n" + codexTestBlock() + "\n" + codexTestTail

	tests := []struct {
		name string
		mode codexEditMode
		want string
	}{
		{"install", codexModeInstall, current},
		{"refresh", codexModeRefresh, current},
		{"uninstall", codexModeUninstall, codexTestHead + "\n" + codexTestTail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := mustTransform(t, input, tt.mode)
			if got != tt.want {
				t.Fatalf("every non-confab line must stay identical and in order\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
			assertNoMarkers(t, got)
		})
	}
}

func TestCodexTransformFreshInstall(t *testing.T) {
	block := codexTestBlock()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty config", "", "[features]\nhooks = true\n\n" + block},
		{
			"user config without hooks",
			"[projects.\"/repo\"]\ntrust_level = \"trusted\"\n",
			"[projects.\"/repo\"]\ntrust_level = \"trusted\"\n\n[features]\nhooks = true\n\n" + block,
		},
		{
			"existing features table gains the flag",
			"[features]\nweb_search = true\n\n[tui]\ntheme = \"dark\"\n",
			"[features]\nhooks = true\nweb_search = true\n\n[tui]\ntheme = \"dark\"\n\n" + block,
		},
		{
			"missing trailing newline",
			"model = \"gpt-5\"",
			"model = \"gpt-5\"\n\n[features]\nhooks = true\n\n" + block,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := mustTransform(t, tt.input, codexModeInstall)
			if got != tt.want {
				t.Fatalf("fresh install output mismatch\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
			assertOneConfabHookSet(t, got)
		})
	}
}

func TestCodexTransformIsIdempotent(t *testing.T) {
	inputs := map[string]string{
		"empty":       "",
		"real shaped": realShapedCodexConfig(t),
		"user group":  codexTestUserGroup,
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			once, _ := mustTransform(t, input, codexModeInstall)
			twice, _ := mustTransform(t, once, codexModeInstall)
			if once != twice {
				t.Fatalf("install twice must equal install once\nonce:\n%s\ntwice:\n%s", once, twice)
			}
			refreshed, _ := mustTransform(t, once, codexModeRefresh)
			if refreshed != once {
				t.Fatalf("refresh of an up-to-date config must change nothing\ngot:\n%s\nwant:\n%s", refreshed, once)
			}
			removed, _ := mustTransform(t, once, codexModeUninstall)
			removedTwice, _ := mustTransform(t, removed, codexModeUninstall)
			if removed != removedTwice {
				t.Fatalf("uninstall twice must equal uninstall once\nonce:\n%s\ntwice:\n%s", removed, removedTwice)
			}
			assertNoConfabHooks(t, removed)
		})
	}
}

// A pre-CF-492 install has only the SessionStart hook. Installing must keep
// that group where it is and add the two missing events.
func TestCodexTransformUpgradesSessionStartOnlyInstall(t *testing.T) {
	p := codexPieces(t, testCodexPath, codexHookGroupIndices{})
	old := codexPieces(t, testCodexPath, codexHookGroupIndices{})
	old.groups[0] = strings.Replace(old.groups[0], codexSessionStartStatusMessage, "Starting Confab sync", 1)
	input := "[features]\nhooks = true\n\n" + confabCodexHooksStart + "\n" + old.groups[0] + "\n" +
		`[hooks.state."` + testCodexPath + `:session_start:0:0"]` + "\ntrusted_hash = \"sha256:old\"\n" +
		confabCodexHooksEnd + "\n\n[tui]\ntheme = \"dark\"\n"

	got, _ := mustTransform(t, input, codexModeInstall)
	want := "[features]\nhooks = true\n\n" + p.groups[0] + "\n" + p.states[0] + "\n[tui]\ntheme = \"dark\"\n\n" +
		p.groups[1] + "\n" + p.groups[2] + "\n" + p.states[1] + "\n" + p.states[2]
	if got != want {
		t.Fatalf("a SessionStart-only install must be upgraded in place\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// A nan float never equals itself; an untouched one must not make the
// pre-write verification refuse the edit.
func TestCodexTransformAcceptsNaNInForeignConfig(t *testing.T) {
	input := "[tuning]\nweight = nan\nlimits = [1.5, nan]\n"
	got, _ := mustTransform(t, input, codexModeInstall)
	if !strings.HasPrefix(got, input) {
		t.Fatalf("foreign config must stay byte-identical\n%s", got)
	}
	assertOneConfabHookSet(t, got)
	if removed, _ := mustTransform(t, got, codexModeUninstall); removed != input+"\n[features]\nhooks = true\n" {
		t.Fatalf("uninstall mismatch\n%s", removed)
	}
}

// A user group that precedes confab's pushes confab's trust key to index 1.
func TestCodexTransformTrustKeyFollowsUserGroupBefore(t *testing.T) {
	got, _ := mustTransform(t, codexTestUserGroup, codexModeInstall)
	if want := `[hooks.state."` + testCodexPath + `:session_start:1:0"]`; !strings.Contains(got, want) {
		t.Fatalf("expected trust key %q\n%s", want, got)
	}
	if notWant := `[hooks.state."` + testCodexPath + `:session_start:0:0"]`; strings.Contains(got, notWant) {
		t.Fatalf("confab trust key must not claim the user's group index: %q\n%s", notWant, got)
	}
	if !strings.HasPrefix(got, codexTestUserGroup) {
		t.Fatalf("user group must stay first and byte-identical\n%s", got)
	}
}

// userGroupAfterConfab is an install whose SessionStart group is followed by
// a user group with its own trust state at index 1.
func userGroupAfterConfab(t *testing.T) (input, userState string) {
	t.Helper()
	p := codexPieces(t, testCodexPath, codexHookGroupIndices{})
	userState = `[hooks.state."` + testCodexPath + `:session_start:1:0"]` + "\ntrusted_hash = \"sha256:user\"\n"
	input = "[features]\nhooks = true\n\n" +
		p.groups[0] + "\n" + codexTestUserGroup + "\n" + p.groups[1] + "\n" + p.groups[2] + "\n" +
		p.states[0] + "\n" + userState + "\n" + p.states[1] + "\n" + p.states[2]
	return input, userState
}

func TestCodexTransformReinstallLeavesUserGroupAfterConfabUntouched(t *testing.T) {
	input, _ := userGroupAfterConfab(t)
	outdated := strings.Replace(input, codexSessionStartStatusMessage, "Starting Confab sync", 1)

	got, notices := mustTransform(t, outdated, codexModeInstall)
	if got != input {
		t.Fatalf("reinstall must replace confab's group in place and leave the user's index and state untouched\ngot:\n%s\nwant:\n%s", got, input)
	}
	if len(notices) != 0 {
		t.Fatalf("no user hook moved, want no notices, got %q", notices)
	}
}

func TestCodexTransformUninstallShiftsUserGroupAndSaysSo(t *testing.T) {
	input, userState := userGroupAfterConfab(t)

	got, notices := mustTransform(t, input, codexModeUninstall)
	want := "[features]\nhooks = true\n\n" + codexTestUserGroup + "\n" + userState
	if got != want {
		t.Fatalf("uninstall must remove only confab's tables\ngot:\n%s\nwant:\n%s", got, want)
	}
	assertNotice(t, notices, "SessionStart", "trust")
}

// A leftover state table already sitting at the key confab is about to use
// becomes confab's: it is rewritten in place, never duplicated.
func TestCodexTransformReplacesStaleStateTableAtTargetKey(t *testing.T) {
	key := `[hooks.state."` + testCodexPath + `:session_start:1:0"]`
	input := codexTestUserGroup + "\n" + key + "\ntrusted_hash = \"sha256:stale\"\nenabled = false\n\n[tui]\ntheme = \"dark\"\n"

	got, _ := mustTransform(t, input, codexModeInstall)
	if n := strings.Count(got, key); n != 1 {
		t.Fatalf("want exactly one table at the target key, got %d\n%s", n, got)
	}
	if strings.Contains(got, "sha256:stale") || strings.Contains(got, "enabled = false") {
		t.Fatalf("install must reset the stale state table\n%s", got)
	}
	if strings.Index(got, key) > strings.Index(got, "[tui]") {
		t.Fatalf("stale state table must be rewritten in place, not moved\n%s", got)
	}
	assertOneConfabHookSet(t, got)
}

func TestCodexTransformHealsDuplicatesAndBrokenMarkers(t *testing.T) {
	block := codexTestBlock()
	second := codexHooksTOML(testCodexPath, testCodexBin, codexHookGroupIndices{sessionStart: 1, preToolUse: 1, postToolUse: 1})
	want := codexTestHead + "\n" + block

	tests := []struct {
		name  string
		input string
	}{
		{
			"two confab blocks",
			codexTestHead + "\n" + confabCodexHooksStart + "\n" + block + confabCodexHooksEnd + "\n\n" +
				confabCodexHooksStart + "\n" + second + confabCodexHooksEnd + "\n",
		},
		{"start marker without end marker", codexTestHead + "\n" + confabCodexHooksStart + "\n" + block},
		{"end marker without start marker", codexTestHead + "\n" + block + confabCodexHooksEnd + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := mustTransform(t, tt.input, codexModeInstall)
			if got != want {
				t.Fatalf("want exactly one confab hook set and no markers\ngot:\n%s\nwant:\n%s", got, want)
			}
			removed, _ := mustTransform(t, tt.input, codexModeUninstall)
			if removed != codexTestHead {
				t.Fatalf("uninstall must leave only the user's config\ngot:\n%s\nwant:\n%s", removed, codexTestHead)
			}
		})
	}
}

// mixedGroupConfig has one PreToolUse group holding confab's handler
// followed by a user handler, each with a trust-state table.
func mixedGroupConfig() (input, userHandler, userState string) {
	userHandler = "[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = \"/usr/bin/other pre\"\n"
	userState = `[hooks.state."` + testCodexPath + `:pre_tool_use:0:1"]` + "\ntrusted_hash = \"sha256:user\"\n"
	input = "[features]\nhooks = true\n\n" +
		"[[hooks.PreToolUse]]\nmatcher = \"Bash\"\n" +
		"[[hooks.PreToolUse.hooks]]\ntype = \"command\"\ncommand = \"" + testCodexBin + " hook pre-tool-use --provider codex\"\nstatusMessage = \"\"\n" +
		userHandler + "\n" +
		`[hooks.state."` + testCodexPath + `:pre_tool_use:0:0"]` + "\ntrusted_hash = \"sha256:confab\"\n\n" +
		userState
	return input, userHandler, userState
}

func TestCodexTransformMixedGroupRemovesOnlyConfabHandler(t *testing.T) {
	input, userHandler, userState := mixedGroupConfig()

	t.Run("uninstall", func(t *testing.T) {
		got, notices := mustTransform(t, input, codexModeUninstall)
		want := "[features]\nhooks = true\n\n[[hooks.PreToolUse]]\nmatcher = \"Bash\"\n" + userHandler + "\n" + userState
		if got != want {
			t.Fatalf("uninstall must remove only confab's handler table and its state entry\ngot:\n%s\nwant:\n%s", got, want)
		}
		assertNotice(t, notices, "PreToolUse", "trust")
	})

	t.Run("install", func(t *testing.T) {
		got, notices := mustTransform(t, input, codexModeInstall)
		assertOneConfabHookSet(t, got)
		for _, want := range []string{userHandler, userState, `[hooks.state."` + testCodexPath + `:pre_tool_use:1:0"]`} {
			if !strings.Contains(got, want) {
				t.Errorf("install output missing %q\n%s", want, got)
			}
		}
		if strings.Contains(got, "sha256:confab") {
			t.Errorf("state entry of the removed confab handler must be removed\n%s", got)
		}
		assertNotice(t, notices, "PreToolUse", "trust")
	})
}

func TestCodexTransformFeatureFlagModes(t *testing.T) {
	block := codexTestBlock()
	disabled := "[features]\nhooks = false # mine\n\n" + block

	t.Run("install sets hooks true", func(t *testing.T) {
		got, _ := mustTransform(t, disabled, codexModeInstall)
		if want := "[features]\nhooks = true # mine\n\n" + block; got != want {
			t.Fatalf("install must flip only the flag value\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("refresh keeps hooks false and says so", func(t *testing.T) {
		got, notices := mustTransform(t, disabled, codexModeRefresh)
		if got != disabled {
			t.Fatalf("refresh must leave an explicit hooks = false alone\ngot:\n%s\nwant:\n%s", got, disabled)
		}
		assertNotice(t, notices, "hooks = false")
	})

	t.Run("uninstall leaves the flag", func(t *testing.T) {
		got, _ := mustTransform(t, "[features]\nhooks = true\n\n"+block, codexModeUninstall)
		if want := "[features]\nhooks = true\n"; got != want {
			t.Fatalf("uninstall must not touch [features]\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("deprecated codex_hooks is removed from features only", func(t *testing.T) {
		input := "[features]\ncodex_hooks = false\n\n[tools]\nhooks = false\ncodex_hooks = true\n"
		got, _ := mustTransform(t, input, codexModeInstall)
		if want := "[features]\nhooks = true\n\n[tools]\nhooks = false\ncodex_hooks = true\n\n" + block; got != want {
			t.Fatalf("flag edits must be scoped to [features]\ngot:\n%s\nwant:\n%s", got, want)
		}
	})
}

func TestCodexTransformStateTableModes(t *testing.T) {
	p := codexPieces(t, testCodexPath, codexHookGroupIndices{})
	header := strings.SplitN(p.states[0], "\n", 2)[0]
	withExtras := header + "\nenabled = false\ntrusted_hash = \"sha256:old\"\ntrusted_content_hash = \"sha256:content\"\n"
	input := "[features]\nhooks = true\n\n" + strings.Replace(codexTestBlock(), p.states[0], withExtras, 1)
	if !strings.Contains(input, "sha256:old") {
		t.Fatalf("fixture did not replace the session-start state table\n%s", input)
	}
	hashLine := strings.SplitN(p.states[0], "\n", 2)[1]

	t.Run("install resets the table", func(t *testing.T) {
		got, _ := mustTransform(t, input, codexModeInstall)
		if want := "[features]\nhooks = true\n\n" + codexTestBlock(); got != want {
			t.Fatalf("install must rewrite confab's state table with only trusted_hash\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("refresh preserves extra keys and says the hook is disabled", func(t *testing.T) {
		got, notices := mustTransform(t, input, codexModeRefresh)
		want := strings.Replace(input, "trusted_hash = \"sha256:old\"\n", hashLine, 1)
		if got != want {
			t.Fatalf("refresh must update only trusted_hash\ngot:\n%s\nwant:\n%s", got, want)
		}
		assertNotice(t, notices, "SessionStart", "disabled")
	})

	t.Run("refresh adds a missing trusted_hash", func(t *testing.T) {
		noHash := strings.Replace(input, "trusted_hash = \"sha256:old\"\n", "", 1)
		got, _ := mustTransform(t, noHash, codexModeRefresh)
		state := mustParseTOML(t, got)["hooks"].(map[string]any)["state"].(map[string]any)[testCodexPath+":session_start:0:0"].(map[string]any)
		if state["trusted_hash"] == nil || state["enabled"] != false || state["trusted_content_hash"] != "sha256:content" {
			t.Fatalf("refresh must add trusted_hash and keep the other keys, got %v\n%s", state, got)
		}
	})
}

func TestCodexTransformParserHazards(t *testing.T) {
	t.Run("multi-line string with header-like lines and marker text", func(t *testing.T) {
		tricky := "[notes]\ntext = \"\"\"\n[[hooks.SessionStart]]\n" + confabCodexHooksStart + "\n[features]\nhooks = false\n" + confabCodexHooksEnd + "\n\"\"\"\n"
		got, _ := mustTransform(t, tricky, codexModeInstall)
		if want := tricky + "\n[features]\nhooks = true\n\n" + codexTestBlock(); got != want {
			t.Fatalf("text inside a multi-line string must stay byte-identical\ngot:\n%s\nwant:\n%s", got, want)
		}
		removed, _ := mustTransform(t, got, codexModeUninstall)
		if want := tricky + "\n[features]\nhooks = true\n"; removed != want {
			t.Fatalf("uninstall must not cut inside a multi-line string\ngot:\n%s\nwant:\n%s", removed, want)
		}
	})

	t.Run("quoted keys with escapes", func(t *testing.T) {
		const path = `/tmp/codex "quoted"/config.toml`
		installed, _, err := transformCodexConfig("", path, testCodexBin, codexModeInstall)
		if err != nil {
			t.Fatalf("install: %v", err)
		}
		if want := `[hooks.state."/tmp/codex \"quoted\"/config.toml:session_start:0:0"]`; !strings.Contains(installed, want) {
			t.Fatalf("expected escaped trust key %q\n%s", want, installed)
		}
		again, _, err := transformCodexConfig(installed, path, testCodexBin, codexModeInstall)
		if err != nil || again != installed {
			t.Fatalf("reinstall must recognise its own escaped state keys (err=%v)\ngot:\n%s\nwant:\n%s", err, again, installed)
		}
		removed, _, err := transformCodexConfig(installed, path, testCodexBin, codexModeUninstall)
		if err != nil || removed != "[features]\nhooks = true\n" {
			t.Fatalf("uninstall must remove escaped state tables (err=%v)\n%s", err, removed)
		}
	})

	t.Run("trailing same-line comments and odd header spacing", func(t *testing.T) {
		input := "[ features ] # flags\nhooks = true # on\n\n" +
			"[[ hooks . \"SessionStart\" ]] # confab\nmatcher = \"startup\" # old\n" +
			"[[hooks.SessionStart.hooks]] # handler\ntype = \"command\" # t\ncommand = \"" + testCodexBin + " hook session-start --provider codex\" # c\n\n" +
			"[tui] # ui\ntheme = \"dark\" # keep\n"
		got, _ := mustTransform(t, input, codexModeUninstall)
		if want := "[ features ] # flags\nhooks = true # on\n\n[tui] # ui\ntheme = \"dark\" # keep\n"; got != want {
			t.Fatalf("a table's span includes its trailing comments and nothing else\ngot:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("CRLF line endings", func(t *testing.T) {
		crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
		input := crlf(codexTestHead + "\n" + confabCodexHooksStart + "\n" + codexTestOldBlock(t) + "\n" + codexTestTail + confabCodexHooksEnd + "\n")
		got, _ := mustTransform(t, input, codexModeInstall)
		if want := crlf(codexTestHead + "\n" + codexTestBlock() + "\n" + codexTestTail); got != want {
			t.Fatalf("CRLF config must keep CRLF endings and its foreign lines\ngot:\n%q\nwant:\n%q", got, want)
		}
		removed, _ := mustTransform(t, input, codexModeUninstall)
		if want := crlf(codexTestHead + "\n" + codexTestTail); removed != want {
			t.Fatalf("CRLF uninstall mismatch\ngot:\n%q\nwant:\n%q", removed, want)
		}
	})

	t.Run("standalone comments next to confab tables survive", func(t *testing.T) {
		input := "# my notes\n" + codexTestBlock() + "# end of file note\n"
		got, _ := mustTransform(t, input, codexModeUninstall)
		if want := "# my notes\n\n# end of file note\n"; got != want {
			t.Fatalf("user comments must survive uninstall\ngot:\n%s\nwant:\n%s", got, want)
		}
	})
}

func TestCodexTransformRefusals(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			"confab hooks in inline-array syntax",
			"[hooks]\nSessionStart = [{ matcher = \"startup\", hooks = [{ type = \"command\", command = \"" + testCodexBin + " hook session-start --provider codex\" }] }]\n",
			"hooks.SessionStart",
		},
		{
			"confab handler in an inline hooks array",
			"[[hooks.PreToolUse]]\nmatcher = \"Bash\"\nhooks = [{ type = \"command\", command = \"" + testCodexBin + " hook pre-tool-use --provider codex\" }]\n",
			"hooks.PreToolUse",
		},
		{"unparseable TOML", "[features\nhooks = true\n", "not valid TOML"},
	}
	for _, tt := range tests {
		for _, mode := range []codexEditMode{codexModeInstall, codexModeRefresh, codexModeUninstall} {
			t.Run(fmt.Sprintf("%s/mode %d", tt.name, mode), func(t *testing.T) {
				_, _, err := transformCodexConfig(tt.input, testCodexPath, testCodexBin, mode)
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("transform must refuse to edit, want error containing %q, got %v", tt.wantErr, err)
				}
			})
		}
	}
}

func TestVerifyCodexEditRefusesUnintendedChanges(t *testing.T) {
	input := realShapedCodexConfig(t)
	good, _ := mustTransform(t, input, codexModeInstall)

	tests := []struct {
		name    string
		output  string
		mode    codexEditMode
		wantErr string
	}{
		{"dropped foreign table", strings.Replace(good, "[desktop]\nsidebar = true\n", "", 1), codexModeInstall, "desktop"},
		{"changed foreign value", strings.Replace(good, `theme = "dark"`, `theme = "light"`, 1), codexModeInstall, "tui.theme"},
		{"added foreign key", good + "\n[extra]\nx = 1\n", codexModeInstall, "extra"},
		{"output is not TOML", good + "\n[broken\n", codexModeInstall, "valid TOML"},
		{"confab hook missing after install", codexTestHead + "\n" + codexTestTail, codexModeInstall, "SessionStart"},
		{"wrong trusted hash after install", strings.Replace(good, "trusted_hash = \"sha256:", "trusted_hash = \"sha256:0", 1), codexModeInstall, "trusted_hash"},
		{"confab hook left after uninstall", good, codexModeUninstall, "SessionStart"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyCodexEdit(input, tt.output, testCodexPath, testCodexBin, tt.mode)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("verification must refuse the write, want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

// --- file-level write path ---

func testCodexEditor(notify io.Writer) codexEditor {
	return codexEditor{binPath: testCodexBin, transform: transformCodexConfig, notify: notify}
}

func writeCodexConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func readConfigText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return string(data)
}

func codexBackups(t *testing.T, configPath string) []string {
	t.Helper()
	matches, err := filepath.Glob(configPath + ".confab-backup-*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	return matches
}

func TestCodexEditorWritesBackupAndAnnouncesIt(t *testing.T) {
	path := writeCodexConfig(t, codexTestHead)
	var notices bytes.Buffer
	if err := testCodexEditor(&notices).apply(path, codexModeInstall); err != nil {
		t.Fatalf("install: %v", err)
	}
	backups := codexBackups(t, path)
	if len(backups) != 1 {
		t.Fatalf("want one backup of the previous config, got %v", backups)
	}
	if got := readConfigText(t, backups[0]); got != codexTestHead {
		t.Fatalf("backup must hold the previous content\n%s", got)
	}
	if !strings.Contains(notices.String(), backups[0]) {
		t.Fatalf("the backup path must be announced, got %q", notices.String())
	}
	assertOneConfabHookSet(t, readConfigText(t, path))
}

func TestCodexEditorNoOpWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	ed := testCodexEditor(io.Discard)
	if err := ed.apply(path, codexModeInstall); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if backups := codexBackups(t, path); len(backups) != 0 {
		t.Fatalf("creating a new config must not write a backup, got %v", backups)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	for _, mode := range []codexEditMode{codexModeInstall, codexModeRefresh} {
		if err := ed.apply(path, mode); err != nil {
			t.Fatalf("no-op apply (mode %d): %v", mode, err)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("an up-to-date config must not be rewritten")
	}
	if backups := codexBackups(t, path); len(backups) != 0 {
		t.Fatalf("a no-op must not create a backup, got %v", backups)
	}
}

func TestCodexEditorUninstallMissingFileIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := testCodexEditor(io.Discard).apply(path, codexModeUninstall); err != nil {
		t.Fatalf("uninstall of a missing config: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("uninstall must not create config.toml (stat err = %v)", err)
	}
}

func TestCodexEditorRefusalsLeaveFileUntouched(t *testing.T) {
	dropForeign := func(input, configPath, binPath string, mode codexEditMode) (string, []string, error) {
		out, notices, err := transformCodexConfig(input, configPath, binPath, mode)
		return strings.Replace(out, "[tui]\ntheme = \"dark\"\n", "", 1), notices, err
	}
	tests := []struct {
		name      string
		content   string
		transform func(string, string, string, codexEditMode) (string, []string, error)
		wantErr   string
	}{
		{
			"inline-array confab hooks",
			"[hooks]\nSessionStart = [{ matcher = \"startup\", hooks = [{ type = \"command\", command = \"" + testCodexBin + " hook session-start --provider codex\" }] }]\n",
			transformCodexConfig, "hooks.SessionStart",
		},
		{"unparseable TOML", "[features\nhooks = true\n", transformCodexConfig, "not valid TOML"},
		{"bad transform fails verification", codexTestHead, dropForeign, "tui"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeCodexConfig(t, tt.content)
			ed := testCodexEditor(io.Discard)
			ed.transform = tt.transform
			err := ed.apply(path, codexModeInstall)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want refusal containing %q, got %v", tt.wantErr, err)
			}
			if !strings.Contains(err.Error(), "not modified") {
				t.Fatalf("the error must say the file is untouched, got %v", err)
			}
			if got := readConfigText(t, path); got != tt.content {
				t.Fatalf("file on disk must be unchanged after a refusal\n%s", got)
			}
			if backups := codexBackups(t, path); len(backups) != 0 {
				t.Fatalf("a refusal must not write a backup, got %v", backups)
			}
		})
	}
}

func TestCodexEditorRedoesEditWhenFileChangesBeforeWrite(t *testing.T) {
	path := writeCodexConfig(t, codexTestHead)
	const late = "\n[late_writer]\nadded = true\n"
	ed := testCodexEditor(io.Discard)
	calls := 0
	ed.beforeRecheck = func() {
		calls++
		if calls == 1 {
			if err := os.WriteFile(path, []byte(codexTestHead+late), 0600); err != nil {
				t.Fatalf("concurrent write: %v", err)
			}
		}
	}
	if err := ed.apply(path, codexModeInstall); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := readConfigText(t, path)
	if !strings.HasPrefix(got, codexTestHead+late) {
		t.Fatalf("the edit must be redone on the other writer's content\n%s", got)
	}
	assertOneConfabHookSet(t, got)
	if calls != 2 {
		t.Fatalf("want one retry (2 attempts), got %d", calls)
	}
}

func TestCodexEditorAbortsWhenFileKeepsChanging(t *testing.T) {
	path := writeCodexConfig(t, codexTestHead)
	ed := testCodexEditor(io.Discard)
	calls := 0
	var last string
	ed.beforeRecheck = func() {
		calls++
		last = codexTestHead + fmt.Sprintf("\n[writer_%d]\nx = 1\n", calls)
		if err := os.WriteFile(path, []byte(last), 0600); err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
	err := ed.apply(path, codexModeInstall)
	if err == nil || !strings.Contains(err.Error(), "being modified by another program") {
		t.Fatalf("want abort naming the concurrent writer, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 3 attempts before aborting, got %d", calls)
	}
	if got := readConfigText(t, path); got != last {
		t.Fatalf("file must be exactly as the other writer left it\n%s", got)
	}
	if backups := codexBackups(t, path); len(backups) != 0 {
		t.Fatalf("an aborted edit must not write a backup, got %v", backups)
	}
}

func TestCodexHooksOutdated(t *testing.T) {
	fresh := func(t *testing.T) (path, content string) {
		t.Helper()
		path = filepath.Join(t.TempDir(), "config.toml")
		if err := testCodexEditor(io.Discard).apply(path, codexModeInstall); err != nil {
			t.Fatalf("install: %v", err)
		}
		return path, readConfigText(t, path)
	}
	mutate := func(t *testing.T, old, new string) string {
		t.Helper()
		path, content := fresh(t)
		mutated := strings.Replace(content, old, new, 1)
		if mutated == content {
			t.Fatalf("fixture mutation %q -> %q changed nothing\n%s", old, new, content)
		}
		if err := os.WriteFile(path, []byte(mutated), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}

	tests := []struct {
		name string
		path func(t *testing.T) string
		bin  string
		want bool
	}{
		{"freshly installed", func(t *testing.T) string { p, _ := fresh(t); return p }, testCodexBin, false},
		{"missing config", func(t *testing.T) string { return filepath.Join(t.TempDir(), "config.toml") }, testCodexBin, false},
		{
			"not fully installed is not outdated",
			func(t *testing.T) string {
				return writeCodexConfig(t, codexPieces(t, testCodexPath, codexHookGroupIndices{}).groups[0])
			},
			testCodexBin, false,
		},
		{"old matcher", func(t *testing.T) string { return mutate(t, `matcher = "Bash"`, `matcher = "Bash|Edit"`) }, testCodexBin, true},
		{
			"old status message",
			func(t *testing.T) string { return mutate(t, codexSessionStartStatusMessage, "Starting Confab sync") },
			testCodexBin, true,
		},
		{"different binary path", func(t *testing.T) string { p, _ := fresh(t); return p }, "/opt/confab/bin/confab", true},
		{
			"stale markers",
			func(t *testing.T) string {
				return mutate(t, "[[hooks.SessionStart]]", confabCodexHooksStart+"\n[[hooks.SessionStart]]")
			},
			testCodexBin, true,
		},
		{"stale trusted hash", func(t *testing.T) string { return mutate(t, `trusted_hash = "sha256:`, `trusted_hash = "sha256:0`) }, testCodexBin, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path(t)
			before := ""
			if data, err := os.ReadFile(path); err == nil {
				before = string(data)
			}
			got, err := codexHooksOutdated(path, tt.bin)
			if err != nil {
				t.Fatalf("codexHooksOutdated() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("codexHooksOutdated() = %v, want %v", got, tt.want)
			}
			if data, err := os.ReadFile(path); err == nil && string(data) != before {
				t.Fatalf("checking for outdated hooks must not write the config")
			}
		})
	}
}

// A binary not named confab must still recognise the hooks it wrote itself,
// so repeated installs never stack duplicate groups.
func TestCodexTransformRecognisesOwnCommandsFromRenamedBinary(t *testing.T) {
	const bin = "/opt/dev/confab-dev"
	once, _, err := transformCodexConfig("", testCodexPath, bin, codexModeInstall)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	twice, _, err := transformCodexConfig(once, testCodexPath, bin, codexModeInstall)
	if err != nil || twice != once {
		t.Fatalf("reinstall from a renamed binary must be a no-op (err=%v)\ngot:\n%s\nwant:\n%s", err, twice, once)
	}
	removed, _, err := transformCodexConfig(once, testCodexPath, bin, codexModeUninstall)
	if err != nil || removed != "[features]\nhooks = true\n" {
		t.Fatalf("uninstall from a renamed binary must remove its own hooks (err=%v)\n%s", err, removed)
	}
}

func TestRefreshCodexHooksUpdatesOutdatedInstall(t *testing.T) {
	path := writeCodexConfig(t, "")
	ed := testCodexEditor(io.Discard)
	if err := ed.apply(path, codexModeInstall); err != nil {
		t.Fatalf("install: %v", err)
	}
	current := readConfigText(t, path)
	outdated := strings.Replace(current, codexSessionStartStatusMessage, "Starting Confab sync", 1) + "\n[desktop]\nsidebar = true\n" + confabCodexHooksEnd
	if err := os.WriteFile(path, []byte(outdated), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ed.apply(path, codexModeRefresh); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got, want := readConfigText(t, path), current+"\n[desktop]\nsidebar = true\n"; got != want {
		t.Fatalf("refresh must update confab's tables and keep Codex's\ngot:\n%s\nwant:\n%s", got, want)
	}
	if outdatedNow, err := codexHooksOutdated(path, testCodexBin); err != nil || outdatedNow {
		t.Fatalf("after refresh the hooks must be up to date (outdated=%v, err=%v)", outdatedNow, err)
	}
}
