package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// initGitRepoWithCommit initialises a git repo in a fresh temp dir, sets
// a test identity, writes one file, and creates an initial commit. The
// initial branch is renamed to "main" so callers can reference it
// deterministically regardless of the host's `init.defaultBranch`.
// Returns the temp dir.
func initGitRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, dir, "add", "f.txt")
	runGit(t, dir, "commit", "-m", "init")
	runGit(t, dir, "branch", "-M", "main")
	return dir
}

func TestDetectGitInfo_NotGitRepo(t *testing.T) {
	// Create temp directory (not a git repo)
	tmpDir := t.TempDir()

	info, err := DetectGitInfo(tmpDir)
	if err != nil {
		t.Fatalf("DetectGitInfo() unexpected error: %v", err)
	}

	if info != nil {
		t.Errorf("Expected nil info for non-git directory, got %+v", info)
	}
}

func TestDetectGitInfo_GitRepo(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	// Create temp directory and init git repo
	tmpDir := t.TempDir()

	// Initialize git repo
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.email", "test@example.com")
	runGit(t, tmpDir, "config", "user.name", "Test User")

	// Create a commit
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test content"), 0644)
	runGit(t, tmpDir, "add", "test.txt")
	runGit(t, tmpDir, "commit", "-m", "Initial commit")

	// Add remote
	runGit(t, tmpDir, "remote", "add", "origin", "https://github.com/test/repo.git")

	// Detect git info
	info, err := DetectGitInfo(tmpDir)
	if err != nil {
		t.Fatalf("DetectGitInfo() error: %v", err)
	}

	if info == nil {
		t.Fatal("Expected non-nil info for git repo")
	}

	// Verify fields
	if info.RepoURL != "https://github.com/test/repo.git" {
		t.Errorf("RepoURL = %q, want %q", info.RepoURL, "https://github.com/test/repo.git")
	}

	if info.Branch == "" {
		t.Error("Branch should not be empty")
	}

	if info.CommitSHA == "" {
		t.Error("CommitSHA should not be empty")
	}

	if info.CommitMessage != "Initial commit" {
		t.Errorf("CommitMessage = %q, want %q", info.CommitMessage, "Initial commit")
	}

	if info.Author != "Test User <test@example.com>" {
		t.Errorf("Author = %q, want %q", info.Author, "Test User <test@example.com>")
	}

	// Repo should be clean (no uncommitted changes)
	if info.IsDirty {
		t.Error("IsDirty should be false for clean repo")
	}
}

func TestDetectGitInfo_DirtyRepo(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	// Create temp directory and init git repo
	tmpDir := t.TempDir()

	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.email", "test@example.com")
	runGit(t, tmpDir, "config", "user.name", "Test User")

	// Create a commit
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test content"), 0644)
	runGit(t, tmpDir, "add", "test.txt")
	runGit(t, tmpDir, "commit", "-m", "Initial commit")

	// Make uncommitted changes
	os.WriteFile(testFile, []byte("modified content"), 0644)

	// Detect git info
	info, err := DetectGitInfo(tmpDir)
	if err != nil {
		t.Fatalf("DetectGitInfo() error: %v", err)
	}

	if info == nil {
		t.Fatal("Expected non-nil info for git repo")
	}

	// Repo should be dirty
	if !info.IsDirty {
		t.Error("IsDirty should be true for repo with uncommitted changes")
	}
}

func TestDetectGitInfo_NoRemote(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	// Create temp directory and init git repo (no remote)
	tmpDir := t.TempDir()

	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.email", "test@example.com")
	runGit(t, tmpDir, "config", "user.name", "Test User")

	// Create a commit
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test content"), 0644)
	runGit(t, tmpDir, "add", "test.txt")
	runGit(t, tmpDir, "commit", "-m", "Initial commit")

	// Detect git info
	info, err := DetectGitInfo(tmpDir)
	if err != nil {
		t.Fatalf("DetectGitInfo() error: %v", err)
	}

	if info == nil {
		t.Fatal("Expected non-nil info for git repo")
	}

	// RepoURL should be empty (no remote configured)
	if info.RepoURL != "" {
		t.Errorf("RepoURL should be empty for repo without remote, got %q", info.RepoURL)
	}

	// Other fields should still be populated
	if info.CommitSHA == "" {
		t.Error("CommitSHA should not be empty")
	}
}

func TestIsGitRepo(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	// Not a git repo
	tmpDir := t.TempDir()
	if isGitRepo(tmpDir) {
		t.Error("isGitRepo() returned true for non-git directory")
	}

	// Is a git repo
	runGit(t, tmpDir, "init")
	if !isGitRepo(tmpDir) {
		t.Error("isGitRepo() returned false for git directory")
	}
}

// Helper to run git commands in tests
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(output))
	}
}

func TestGetHeadSHA(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	t.Run("not a git repo", func(t *testing.T) {
		tmpDir := t.TempDir()
		sha, err := GetHeadSHA(tmpDir)
		if err != nil {
			t.Errorf("GetHeadSHA() unexpected error: %v", err)
		}
		if sha != "" {
			t.Errorf("GetHeadSHA() = %q, want empty string for non-git dir", sha)
		}
	})

	t.Run("git repo with commit", func(t *testing.T) {
		tmpDir := t.TempDir()

		runGit(t, tmpDir, "init")
		runGit(t, tmpDir, "config", "user.email", "test@example.com")
		runGit(t, tmpDir, "config", "user.name", "Test User")

		// Create a commit
		testFile := filepath.Join(tmpDir, "test.txt")
		os.WriteFile(testFile, []byte("test content"), 0644)
		runGit(t, tmpDir, "add", "test.txt")
		runGit(t, tmpDir, "commit", "-m", "Initial commit")

		sha, err := GetHeadSHA(tmpDir)
		if err != nil {
			t.Errorf("GetHeadSHA() error: %v", err)
		}
		if sha == "" {
			t.Error("GetHeadSHA() returned empty string for repo with commit")
		}
		// SHA should be 40 hex characters
		if len(sha) != 40 {
			t.Errorf("GetHeadSHA() = %q, expected 40 character SHA", sha)
		}
	})
}

func TestGetRepoURL(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	t.Run("not a git repo", func(t *testing.T) {
		tmpDir := t.TempDir()
		url, err := GetRepoURL(tmpDir)
		if err != nil {
			t.Errorf("GetRepoURL() unexpected error: %v", err)
		}
		if url != "" {
			t.Errorf("GetRepoURL() = %q, want empty string for non-git dir", url)
		}
	})

	t.Run("git repo with remote", func(t *testing.T) {
		tmpDir := t.TempDir()

		runGit(t, tmpDir, "init")
		runGit(t, tmpDir, "remote", "add", "origin", "https://github.com/test/repo.git")

		url, err := GetRepoURL(tmpDir)
		if err != nil {
			t.Errorf("GetRepoURL() error: %v", err)
		}
		if url != "https://github.com/test/repo.git" {
			t.Errorf("GetRepoURL() = %q, want %q", url, "https://github.com/test/repo.git")
		}
	})

	t.Run("git repo without remote", func(t *testing.T) {
		tmpDir := t.TempDir()

		runGit(t, tmpDir, "init")

		url, err := GetRepoURL(tmpDir)
		if err != nil {
			t.Errorf("GetRepoURL() unexpected error: %v", err)
		}
		if url != "" {
			t.Errorf("GetRepoURL() = %q, want empty string for repo without remote", url)
		}
	})
}

func TestParseRemoteVOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []GitRemote
	}{
		{
			name: "empty input",
			in:   "",
			want: nil,
		},
		{
			name: "single remote fetch + push equal URLs",
			in: "origin\tgit@github.com:owner/repo.git (fetch)\n" +
				"origin\tgit@github.com:owner/repo.git (push)\n",
			want: []GitRemote{
				{Name: "origin", FetchURL: "git@github.com:owner/repo.git", PushURL: "git@github.com:owner/repo.git"},
			},
		},
		{
			name: "multi remote preserves first-appearance order",
			in: "origin\tgit@github.com:jackie/repo.git (fetch)\n" +
				"origin\tgit@github.com:jackie/repo.git (push)\n" +
				"upstream\tgit@github.com:ConfabulousDev/repo.git (fetch)\n" +
				"upstream\tgit@github.com:ConfabulousDev/repo.git (push)\n",
			want: []GitRemote{
				{Name: "origin", FetchURL: "git@github.com:jackie/repo.git", PushURL: "git@github.com:jackie/repo.git"},
				{Name: "upstream", FetchURL: "git@github.com:ConfabulousDev/repo.git", PushURL: "git@github.com:ConfabulousDev/repo.git"},
			},
		},
		{
			name: "push URL differs from fetch URL",
			in: "origin\thttps://github.com/owner/repo.git (fetch)\n" +
				"origin\tgit@github.com:owner/repo.git (push)\n",
			want: []GitRemote{
				{Name: "origin", FetchURL: "https://github.com/owner/repo.git", PushURL: "git@github.com:owner/repo.git"},
			},
		},
		{
			name: "trailing blank lines and extra whitespace",
			in:   "origin\thttps://github.com/owner/repo.git (fetch)\norigin\thttps://github.com/owner/repo.git (push)\n\n   \n",
			want: []GitRemote{
				{Name: "origin", FetchURL: "https://github.com/owner/repo.git", PushURL: "https://github.com/owner/repo.git"},
			},
		},
		{
			name: "malformed line without tab is skipped",
			in: "this is not valid\n" +
				"origin\thttps://github.com/owner/repo.git (fetch)\n" +
				"origin\thttps://github.com/owner/repo.git (push)\n",
			want: []GitRemote{
				{Name: "origin", FetchURL: "https://github.com/owner/repo.git", PushURL: "https://github.com/owner/repo.git"},
			},
		},
		{
			name: "entry with empty name is dropped (CF-494 guard)",
			in:   "\thttps://github.com/owner/repo.git (fetch)\n",
			want: nil,
		},
		{
			name: "entry with both URLs empty is dropped (CF-494 guard)",
			in:   "origin\t (fetch)\norigin\t (push)\n",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRemoteVOutput(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("parseRemoteVOutput(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectRemotes_NotGitRepo(t *testing.T) {
	tmpDir := t.TempDir()
	got, err := DetectRemotes(tmpDir)
	if err != nil {
		t.Errorf("DetectRemotes(non-git) error = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("DetectRemotes(non-git) = %+v, want nil", got)
	}
}

func TestDetectRemotes_SingleRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := t.TempDir()
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "remote", "add", "origin", "git@github.com:owner/repo.git")

	got, err := DetectRemotes(tmpDir)
	if err != nil {
		t.Fatalf("DetectRemotes error: %v", err)
	}
	want := []GitRemote{
		{Name: "origin", FetchURL: "git@github.com:owner/repo.git", PushURL: "git@github.com:owner/repo.git"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("DetectRemotes = %+v, want %+v", got, want)
	}
}

func TestDetectRemotes_MultiRemoteWithUpstream(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := t.TempDir()
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "remote", "add", "origin", "git@github.com:jackie/repo.git")
	runGit(t, tmpDir, "remote", "add", "upstream", "git@github.com:ConfabulousDev/repo.git")

	got, err := DetectRemotes(tmpDir)
	if err != nil {
		t.Fatalf("DetectRemotes error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("DetectRemotes returned %d entries, want 2: %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name}
	if names[0] != "origin" || names[1] != "upstream" {
		t.Errorf("DetectRemotes order = %v, want [origin upstream]", names)
	}
	if got[0].FetchURL != "git@github.com:jackie/repo.git" {
		t.Errorf("origin fetch URL = %q", got[0].FetchURL)
	}
	if got[1].FetchURL != "git@github.com:ConfabulousDev/repo.git" {
		t.Errorf("upstream fetch URL = %q", got[1].FetchURL)
	}
}

func TestDetectRemotes_PushFetchMismatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := t.TempDir()
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "remote", "add", "origin", "https://github.com/owner/repo.git")
	runGit(t, tmpDir, "remote", "set-url", "--push", "origin", "git@github.com:owner/repo.git")

	got, err := DetectRemotes(tmpDir)
	if err != nil {
		t.Fatalf("DetectRemotes error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %+v", got)
	}
	if got[0].FetchURL != "https://github.com/owner/repo.git" {
		t.Errorf("FetchURL = %q, want HTTPS", got[0].FetchURL)
	}
	if got[0].PushURL != "git@github.com:owner/repo.git" {
		t.Errorf("PushURL = %q, want SSH", got[0].PushURL)
	}
}

func TestDetectTrackingRemote_Configured(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := initGitRepoWithCommit(t)
	runGit(t, tmpDir, "config", "branch.main.remote", "upstream")

	got := DetectTrackingRemote(tmpDir, "main")
	if got != "upstream" {
		t.Errorf("DetectTrackingRemote = %q, want %q", got, "upstream")
	}
}

func TestDetectTrackingRemote_NotConfigured(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := t.TempDir()
	runGit(t, tmpDir, "init")
	got := DetectTrackingRemote(tmpDir, "main")
	if got != "" {
		t.Errorf("DetectTrackingRemote unset = %q, want empty", got)
	}
}

func TestDetectTrackingRemote_EmptyBranch(t *testing.T) {
	tmpDir := t.TempDir()
	got := DetectTrackingRemote(tmpDir, "")
	if got != "" {
		t.Errorf("DetectTrackingRemote(\"\") = %q, want empty", got)
	}
}

func TestDetectTrackingRemote_DetachedHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := t.TempDir()
	runGit(t, tmpDir, "init")
	got := DetectTrackingRemote(tmpDir, "HEAD")
	if got != "" {
		t.Errorf("DetectTrackingRemote(HEAD) = %q, want empty", got)
	}
}

func TestDetectBranch_OnBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := initGitRepoWithCommit(t)
	runGit(t, tmpDir, "checkout", "-b", "feature")

	got := DetectBranch(tmpDir)
	if got != "feature" {
		t.Errorf("DetectBranch = %q, want %q", got, "feature")
	}
}

func TestDetectBranch_DetachedHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := initGitRepoWithCommit(t)
	// Detach HEAD by checking out the commit SHA directly.
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = tmpDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	sha := strings.TrimSpace(string(out))
	runGit(t, tmpDir, "checkout", sha)

	got := DetectBranch(tmpDir)
	if got != "HEAD" {
		t.Errorf("DetectBranch(detached) = %q, want %q", got, "HEAD")
	}
}

func TestDetectBranch_NotGitRepo(t *testing.T) {
	tmpDir := t.TempDir()
	got := DetectBranch(tmpDir)
	if got != "" {
		t.Errorf("DetectBranch(non-git) = %q, want empty", got)
	}
}

func TestDetectGitInfo_PopulatesRemotesAndTracking(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmpDir := initGitRepoWithCommit(t)
	runGit(t, tmpDir, "remote", "add", "origin", "git@github.com:jackie/repo.git")
	runGit(t, tmpDir, "remote", "add", "upstream", "git@github.com:ConfabulousDev/repo.git")
	runGit(t, tmpDir, "config", "branch.main.remote", "upstream")

	info, err := DetectGitInfo(tmpDir)
	if err != nil {
		t.Fatalf("DetectGitInfo: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil GitInfo")
	}
	if len(info.Remotes) != 2 {
		t.Fatalf("expected 2 remotes, got %d: %+v", len(info.Remotes), info.Remotes)
	}
	if info.Remotes[0].Name != "origin" || info.Remotes[1].Name != "upstream" {
		t.Errorf("remote order = [%s %s], want [origin upstream]",
			info.Remotes[0].Name, info.Remotes[1].Name)
	}
	if info.TrackingRemote != "upstream" {
		t.Errorf("TrackingRemote = %q, want %q", info.TrackingRemote, "upstream")
	}
}

func TestToGitHubURL(t *testing.T) {
	tests := []struct {
		name   string
		gitURL string
		want   string
	}{
		{
			name:   "SSH format",
			gitURL: "git@github.com:owner/repo.git",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "SSH format without .git",
			gitURL: "git@github.com:owner/repo",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "HTTPS format",
			gitURL: "https://github.com/owner/repo.git",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "HTTPS format without .git",
			gitURL: "https://github.com/owner/repo",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "SSH URL format",
			gitURL: "ssh://git@github.com/owner/repo.git",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "HTTP format",
			gitURL: "http://github.com/owner/repo.git",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "GitLab URL (not GitHub)",
			gitURL: "git@gitlab.com:owner/repo.git",
			want:   "",
		},
		{
			name:   "Bitbucket URL (not GitHub)",
			gitURL: "git@bitbucket.org:owner/repo.git",
			want:   "",
		},
		{
			name:   "empty string",
			gitURL: "",
			want:   "",
		},
		{
			name:   "whitespace",
			gitURL: "  https://github.com/owner/repo.git  ",
			want:   "https://github.com/owner/repo",
		},
		{
			name:   "org with hyphen",
			gitURL: "git@github.com:my-org/my-repo.git",
			want:   "https://github.com/my-org/my-repo",
		},
		{
			name:   "nested path (enterprise)",
			gitURL: "https://github.com/org/team/repo.git",
			want:   "https://github.com/org/team/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToGitHubURL(tt.gitURL)
			if got != tt.want {
				t.Errorf("ToGitHubURL(%q) = %q, want %q", tt.gitURL, got, tt.want)
			}
		})
	}
}
