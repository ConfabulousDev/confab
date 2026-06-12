package pathcanon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCanonicalDirTrailingSlashAndDots: trailing slashes and dot-segments
// must normalize away so two spellings of one dir match.
func TestCanonicalDirTrailingSlashAndDots(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "child")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	want := CanonicalDir(sub)

	if got := CanonicalDir(sub + "/"); got != want {
		t.Errorf("trailing slash: CanonicalDir(%q)=%q, want %q", sub+"/", got, want)
	}
	dotted := filepath.Join(dir, "child", ".", "..", "child")
	if got := CanonicalDir(dotted); got != want {
		t.Errorf("dot segments: CanonicalDir(%q)=%q, want %q", dotted, got, want)
	}
}

// TestCanonicalDirTilde: a leading ~ must expand to the home directory.
func TestCanonicalDirTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if got, want := CanonicalDir("~"), CanonicalDir(home); got != want {
		t.Errorf("CanonicalDir(\"~\")=%q, want %q", got, want)
	}
}

// TestCanonicalDirSymlink: a symlinked dir and its real target must
// canonicalize to the same string (EvalSymlinks both ends). This is the
// core de-risk for the derivation matching contract.
func TestCanonicalDirSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if got, want := CanonicalDir(link), CanonicalDir(real); got != want {
		t.Errorf("symlink not resolved: CanonicalDir(%q)=%q, want %q", link, got, want)
	}
}

// TestCanonicalDirIdempotent: canonicalizing an already-canonical path is a
// no-op.
func TestCanonicalDirIdempotent(t *testing.T) {
	dir := t.TempDir()
	once := CanonicalDir(dir)
	if twice := CanonicalDir(once); twice != once {
		t.Errorf("not idempotent: CanonicalDir(%q)=%q != %q", once, twice, once)
	}
}
