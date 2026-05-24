package git

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/types"
)

// GitInfo contains git repository information
type GitInfo struct {
	RepoURL        string      `json:"repo_url,omitempty"`
	Branch         string      `json:"branch,omitempty"`
	CommitSHA      string      `json:"commit_sha,omitempty"`
	CommitMessage  string      `json:"commit_message,omitempty"`
	Author         string      `json:"author,omitempty"`
	IsDirty        bool        `json:"is_dirty"`
	Remotes        []GitRemote `json:"remotes,omitempty"`
	TrackingRemote string      `json:"tracking_remote,omitempty"`
}

// GitRemote describes a single git remote (one entry in `git remote -v`,
// merging the (fetch) and (push) lines). JSON tags mirror CF-494's
// db.GitRemote exactly — no omitempty on URL fields, since the CLI always
// emits both per the locked wire-format decision.
type GitRemote struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetch_url"`
	PushURL  string `json:"push_url"`
}

// parseRemoteVOutput parses the output of `git remote -v` into merged
// GitRemote entries. Lines look like:
//
//	origin\tgit@github.com:owner/repo.git (fetch)
//	origin\tgit@github.com:owner/repo.git (push)
//
// Entries are merged by name in order of first appearance. Defensive
// guards (CF-494 wire-format compat): drop any entry whose Name is empty
// or whose FetchURL and PushURL are both empty — the backend 400s the
// chunk on either condition.
func parseRemoteVOutput(out string) []GitRemote {
	byName := make(map[string]*GitRemote)
	var order []string

	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 {
			continue // malformed: no tab, or empty name
		}
		name := line[:tab]
		rest := line[tab+1:]

		// rest looks like "URL (fetch)" or "URL (push)".
		var direction string
		switch {
		case strings.HasSuffix(rest, " (fetch)"):
			direction = "fetch"
			rest = strings.TrimSuffix(rest, " (fetch)")
		case strings.HasSuffix(rest, " (push)"):
			direction = "push"
			rest = strings.TrimSuffix(rest, " (push)")
		default:
			continue // malformed: missing direction marker
		}
		url := strings.TrimSpace(rest)

		r, seen := byName[name]
		if !seen {
			r = &GitRemote{Name: name}
			byName[name] = r
			order = append(order, name)
		}
		if direction == "fetch" {
			r.FetchURL = url
		} else {
			r.PushURL = url
		}
	}

	result := make([]GitRemote, 0, len(order))
	for _, name := range order {
		r := byName[name]
		if r.Name == "" || (r.FetchURL == "" && r.PushURL == "") {
			continue
		}
		result = append(result, *r)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// DetectRemotes returns the configured git remotes for cwd. Returns
// (nil, nil) when cwd is not a git repo or when git is unavailable —
// silent best-effort, matching GetRepoURL's pattern.
func DetectRemotes(cwd string) ([]GitRemote, error) {
	if !isGitRepo(cwd) {
		return nil, nil
	}
	out, err := gitCommand(cwd, "remote", "-v")
	if err != nil {
		return nil, nil
	}
	return parseRemoteVOutput(out), nil
}

// DetectTrackingRemote returns the value of branch.<branch>.remote git
// config for the given cwd, or "" when unset / on any error. Silent.
// branch == "" short-circuits to "" without invoking git.
func DetectTrackingRemote(cwd, branch string) string {
	if branch == "" {
		return ""
	}
	out, err := gitCommand(cwd, "config", "--get", "branch."+branch+".remote")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// DetectBranch returns the current branch name (`git rev-parse
// --abbrev-ref HEAD`) for the given cwd, or "" on error. Detached HEAD
// returns the literal string "HEAD".
func DetectBranch(cwd string) string {
	out, err := gitCommand(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// DetectGitInfo detects git information from the given directory
// Returns nil if not in a git repository (this is not an error)
func DetectGitInfo(cwd string) (*GitInfo, error) {
	// Check if we're in a git repository
	if !isGitRepo(cwd) {
		return nil, nil // Not a git repo - not an error
	}

	info := &GitInfo{}

	// Get remote URL
	if url, err := gitCommand(cwd, "config", "--get", "remote.origin.url"); err == nil {
		info.RepoURL = strings.TrimSpace(url)
	}

	// Get current branch
	if branch, err := gitCommand(cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(branch)
	}

	// Get commit SHA
	if sha, err := gitCommand(cwd, "rev-parse", "HEAD"); err == nil {
		info.CommitSHA = strings.TrimSpace(sha)
	}

	// Get commit message
	if msg, err := gitCommand(cwd, "log", "-1", "--format=%s"); err == nil {
		info.CommitMessage = strings.TrimSpace(msg)
	}

	// Get author
	if author, err := gitCommand(cwd, "log", "-1", "--format=%an <%ae>"); err == nil {
		info.Author = strings.TrimSpace(author)
	}

	// Check if repo is dirty (has uncommitted changes)
	if status, err := gitCommand(cwd, "status", "--porcelain"); err == nil {
		info.IsDirty = strings.TrimSpace(status) != ""
	}

	// Configured remotes + tracking remote for the current branch (CF-493 /
	// CF-494 fork→upstream resolution). Best-effort: any error leaves the
	// fields at their zero value, which the JSON omitempty drops.
	// DetectTrackingRemote short-circuits to "" on empty branch.
	info.Remotes, _ = DetectRemotes(cwd)
	info.TrackingRemote = DetectTrackingRemote(cwd, info.Branch)

	return info, nil
}

// isGitRepo checks if the directory is inside a git repository
func isGitRepo(cwd string) bool {
	_, err := gitCommand(cwd, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// gitCommand runs a git command in the specified directory.
//
// Security: While exec.Command is safe against shell injection (args are passed
// directly to the process, not through a shell), callers must only pass trusted,
// hardcoded arguments. Never pass user-controlled data as arguments without
// validation. All current callers in this package use hardcoded string literals.
func gitCommand(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

// GetRepoURL returns the remote origin URL for a git repository
// Returns empty string if not a git repo or no remote configured
func GetRepoURL(cwd string) (string, error) {
	if !isGitRepo(cwd) {
		return "", nil
	}
	url, err := gitCommand(cwd, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", nil // No remote configured
	}
	return strings.TrimSpace(url), nil
}

// GetHeadSHA returns the full SHA of the HEAD commit.
// Returns empty string and nil if not in a git repo.
func GetHeadSHA(cwd string) (string, error) {
	if !isGitRepo(cwd) {
		return "", nil
	}
	sha, err := gitCommand(cwd, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

// ToGitHubURL converts a git remote URL to a GitHub HTTPS URL.
// Handles: git@github.com:owner/repo.git, https://github.com/owner/repo.git,
// ssh://git@github.com/owner/repo.git
// Returns empty string if not a GitHub URL.
func ToGitHubURL(gitURL string) string {
	gitURL = strings.TrimSpace(gitURL)
	if gitURL == "" {
		return ""
	}

	// Remove .git suffix if present
	gitURL = strings.TrimSuffix(gitURL, ".git")

	// Handle SSH format: git@github.com:owner/repo
	if strings.HasPrefix(gitURL, "git@github.com:") {
		path := strings.TrimPrefix(gitURL, "git@github.com:")
		return "https://github.com/" + path
	}

	// Handle SSH URL format: ssh://git@github.com/owner/repo
	if strings.HasPrefix(gitURL, "ssh://git@github.com/") {
		path := strings.TrimPrefix(gitURL, "ssh://git@github.com/")
		return "https://github.com/" + path
	}

	// Handle HTTPS format: https://github.com/owner/repo
	if strings.HasPrefix(gitURL, "https://github.com/") {
		return gitURL
	}

	// Handle HTTP format (less common): http://github.com/owner/repo
	if strings.HasPrefix(gitURL, "http://github.com/") {
		return "https" + strings.TrimPrefix(gitURL, "http")
	}

	return "" // Not a GitHub URL
}

// ExtractGitInfoFromTranscript parses a Claude Code transcript file to extract git information
// This is useful for uploading sessions where the original directory may not exist
func ExtractGitInfoFromTranscript(transcriptPath string) (*GitInfo, error) {
	file, err := os.Open(transcriptPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := types.NewJSONLScanner(file)

	var gitInfo *GitInfo
	var cwd string

	// Scan through transcript looking for git information
	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue // Skip malformed lines
		}

		// Look for gitBranch field in message
		if branch, ok := msg["gitBranch"].(string); ok && branch != "" {
			if gitInfo == nil {
				gitInfo = &GitInfo{}
			}
			gitInfo.Branch = branch

			// Also extract cwd if available
			if cwdField, ok := msg["cwd"].(string); ok && cwdField != "" {
				cwd = cwdField
			}

			// Once we have git info, we can stop scanning
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// If we found git info and a cwd, try to get repo URL + remotes +
	// tracking remote from that directory. cwd often doesn't exist anymore
	// when this function is called (it's the transcript-replay path), so
	// every call is silently best-effort. DetectTrackingRemote short-circuits
	// to "" on empty branch.
	if gitInfo != nil && cwd != "" {
		if url, err := GetRepoURL(cwd); err == nil {
			gitInfo.RepoURL = url
		}
		gitInfo.Remotes, _ = DetectRemotes(cwd)
		gitInfo.TrackingRemote = DetectTrackingRemote(cwd, gitInfo.Branch)
	}

	return gitInfo, nil
}
