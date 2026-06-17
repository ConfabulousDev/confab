package cmd

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// handlePreToolUseCursor handles Cursor preToolUse events. Unlike Claude/Codex
// (which deny + instruct the agent to add the Confab link), Cursor rewrites the
// Shell command in place via updated_input — a Cursor-native, no-round-trip
// injection (65aq, spike-confirmed). For a `git commit` it appends a
// `--trailer "Confab-Link: <url>"`; for `gh pr create` it injects the
// `📝 [Confab link](<url>)` line into the PR body. Every other command (and
// any non-Shell tool) is allowed unchanged. Idempotent: a command that already
// carries the link is allowed without a second rewrite.
func handlePreToolUseCursor(p provider.Provider, r io.Reader, w io.Writer) error {
	in, err := types.ReadCursorToolUseHookInput(r)
	if err != nil {
		logger.Warn("Failed to read cursor tool-use hook input: %v", err)
		return nil // Don't block the firing tool.
	}

	// Default response: allow with no rewrite. Any early return emits this.
	allow := func() error { return writeCursorToolUseResponse(w, "allow", nil) }

	if in.ToolName != config.ToolNameCursorShell {
		return allow()
	}
	command, _ := in.ToolInput["command"].(string)
	if command == "" {
		return allow()
	}

	commitPos := firstMatch(gitCommitPattern, command)
	prCreatePos := firstMatch(ghPRCreatePattern, command)
	if commitPos < 0 && prCreatePos < 0 {
		return allow()
	}
	isCommit := commitPos >= 0 && (prCreatePos < 0 || commitPos < prCreatePos)

	confabSessionID, err := getConfabSessionID(p, in.SessionID)
	if err != nil || confabSessionID == "" {
		logger.Warn("Confab link skipped: no session ID available (err=%v)", err)
		return allow()
	}

	cfg, err := uploadConfigForHook(p, in.TranscriptPath)
	if err != nil {
		logger.Warn("Confab link skipped: %v", err)
		return allow()
	}

	sessionURL, err := formatSessionURL(confabSessionID, cfg.BackendURL)
	if err != nil {
		logger.Warn("Confab link skipped: %v", err)
		return allow()
	}

	if commandContainsConfabLink(command, confabSessionID, cfg.BackendURL) {
		logger.Info("Confab link already present in cursor command")
		return allow()
	}

	var rewritten string
	if isCommit {
		rewritten = rewriteCursorCommitCommand(command, sessionURL)
		logger.Info("Rewriting cursor git commit to add Confab link -> session %s", confabSessionID)
	} else {
		rewritten = rewriteCursorPRCommand(command, sessionURL)
		logger.Info("Rewriting cursor gh pr create to add Confab link -> session %s", confabSessionID)
	}

	// If the rewrite produced no change (e.g. an unsupported body form), allow
	// the original command rather than emitting an identical updated_input.
	if rewritten == "" || rewritten == command {
		return allow()
	}

	updated := map[string]any{"command": rewritten}
	// Preserve the other tool_input keys (cwd, timeout) so the rewrite doesn't
	// drop them — Cursor replaces tool_input wholesale with updated_input.
	for k, v := range in.ToolInput {
		if k == "command" {
			continue
		}
		updated[k] = v
	}
	return writeCursorToolUseResponse(w, "allow", updated)
}

// rewriteCursorCommitCommand appends a `--trailer "Confab-Link: <url>"` to the
// git commit invocation. git accepts multiple --trailer flags (Cursor already
// appends its own Co-authored-by trailer), so this coexists cleanly. The
// trailer value is shell-single-quoted (it is our own backend URL, low
// injection risk, but quote correctly).
func rewriteCursorCommitCommand(command, sessionURL string) string {
	trailer := formatTrailerLine(sessionURL) // "Confab-Link: <url>"
	return command + " --trailer " + shellSingleQuote(trailer)
}

// cursorPRBodyFlagRe matches a `--body <value>` or `-b <value>` flag with a
// single- or double-quoted value, capturing the quote char and the inner body
// so we can append the Confab link line inside the existing quotes. The flag
// must be followed by whitespace and a quote, so `--body=...`, `--body-file`,
// and unquoted/substituted bodies do not match (handled by the guard below).
var cursorPRBodyFlagRe = regexp.MustCompile(`(?:^|\s)(--body|-b)(\s+)(['"])((?:\\.|[^\\])*?)(['"])`)

// cursorPRBodyPresentRe detects ANY body-providing flag (--body, -b,
// --body-file, -F, including the `--body=` / `-F=` equals forms) so the
// fall-through can decline rather than append a duplicate --body.
var cursorPRBodyPresentRe = regexp.MustCompile(`(?:^|\s)(--body(?:-file)?|-b|-F)(=|\s|$)`)

// rewriteCursorPRCommand injects the Confab link line into a `gh pr create`
// command. When the command carries a `--body`/`-b` with a quoted value, the
// link line is appended inside that value (preserving the quote style); when no
// body flag is present at all, a `--body "<link>"` is appended. Body forms we
// can't safely rewrite (--body-file, `--body=...`, $(cat …), heredoc) are left
// unchanged (return "" → caller allows the original command) rather than
// mis-rewriting or emitting a duplicate --body.
func rewriteCursorPRCommand(command, sessionURL string) string {
	link := formatPRLink(sessionURL) // 📝 [Confab link](<url>)

	if loc := cursorPRBodyFlagRe.FindStringSubmatchIndex(command); loc != nil {
		// Submatch groups: 1=flag, 2=sep, 3=open quote, 4=body, 5=close quote.
		group := func(i int) string { return command[loc[2*i]:loc[2*i+1]] }
		flag, sep, openQ, body, closeQ := group(1), group(2), group(3), group(4), group(5)
		newBody := body
		if !strings.HasSuffix(newBody, "\n") {
			newBody += "\n"
		}
		newBody += "\n" + link
		replacement := flag + sep + openQ + newBody + closeQ
		// loc[2] is the start of group 1 (the flag), which excludes the leading
		// whitespace anchor so the surrounding command is preserved verbatim.
		return command[:loc[2]] + replacement + command[loc[1]:]
	}

	// A body flag is present but in a form we can't safely rewrite
	// (--body=..., --body-file, -F, unquoted/substituted): leave it unchanged.
	if cursorPRBodyPresentRe.MatchString(command) {
		return ""
	}

	// No body flag at all: append one with just the link.
	return command + " --body " + shellSingleQuote(link)
}

// shellSingleQuote wraps s in POSIX single quotes, escaping embedded single
// quotes via the '\'' idiom so the value is passed to the shell verbatim.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeCursorToolUseResponse writes Cursor's preToolUse response shape:
// {permission, updated_input?}. updatedInput is omitted when nil (no rewrite).
func writeCursorToolUseResponse(w io.Writer, permission string, updatedInput map[string]any) error {
	resp := types.CursorToolUseResponse{Permission: permission, UpdatedInput: updatedInput}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Debug("Failed to write cursor tool-use response: %v", err)
	}
	return nil
}

// handlePostToolUseCursor handles Cursor postToolUse events: link the resulting
// GitHub commit / PR back to the session. The raw shell output rides on
// tool_output.output (a JSON object {output, exitCode}); link-back is skipped on
// a non-zero exit. For a commit the full SHA is re-derived from the repo (git
// rev-parse), mirroring Claude/Codex, so the link uses the full SHA rather than
// the abbreviated SHA in the output line. For a PR the URL is extracted from the
// output text.
func handlePostToolUseCursor(p provider.Provider, r io.Reader, _ io.Writer) error {
	in, err := types.ReadCursorToolUseHookInput(r)
	if err != nil {
		logger.Warn("Failed to read cursor tool-use hook input: %v", err)
		return nil
	}

	if in.ToolName != config.ToolNameCursorShell {
		return nil
	}
	command, _ := in.ToolInput["command"].(string)
	if command == "" {
		return nil
	}

	out, ok := in.ToolOutput()
	if !ok {
		logger.Debug("Cursor postToolUse: no tool_output, skipping link")
		return nil
	}

	if firstMatch(ghPRCreatePattern, command) >= 0 {
		prURL := prURLPattern.FindString(out.Output)
		if prURL == "" {
			logger.Debug("No PR URL found in cursor tool_output")
			return nil
		}
		return linkGitHubURL(p, in.SessionID, prURL, in.TranscriptPath)
	}

	if firstMatch(gitCommitPattern, command) >= 0 || firstMatch(gitPushPattern, command) >= 0 {
		if out.ExitCode != 0 {
			logger.Debug("Cursor git command exited %d, skipping link", out.ExitCode)
			return nil
		}
		return linkCommitToSession(p, in.SessionID, in.CWD, in.TranscriptPath)
	}

	return nil
}
