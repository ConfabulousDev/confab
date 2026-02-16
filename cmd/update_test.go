package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestFindAssetURLForPlatform(t *testing.T) {
	release := &githubRelease{
		TagName: "v0.10.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{
				Name:               "confab_0.10.0_darwin_arm64.tar.gz",
				BrowserDownloadURL: "https://github.com/example/confab/releases/download/v0.10.0/confab_0.10.0_darwin_arm64.tar.gz",
			},
			{
				Name:               "confab_0.10.0_darwin_amd64.tar.gz",
				BrowserDownloadURL: "https://github.com/example/confab/releases/download/v0.10.0/confab_0.10.0_darwin_amd64.tar.gz",
			},
			{
				Name:               "confab_0.10.0_linux_amd64.tar.gz",
				BrowserDownloadURL: "https://github.com/example/confab/releases/download/v0.10.0/confab_0.10.0_linux_amd64.tar.gz",
			},
			{
				Name:               "checksums.txt",
				BrowserDownloadURL: "https://github.com/example/confab/releases/download/v0.10.0/checksums.txt",
			},
		},
	}

	tests := []struct {
		name    string
		goos    string
		goarch  string
		wantURL string
		wantErr bool
	}{
		{
			name:    "darwin/arm64",
			goos:    "darwin",
			goarch:  "arm64",
			wantURL: "https://github.com/example/confab/releases/download/v0.10.0/confab_0.10.0_darwin_arm64.tar.gz",
		},
		{
			name:    "darwin/amd64",
			goos:    "darwin",
			goarch:  "amd64",
			wantURL: "https://github.com/example/confab/releases/download/v0.10.0/confab_0.10.0_darwin_amd64.tar.gz",
		},
		{
			name:    "linux/amd64",
			goos:    "linux",
			goarch:  "amd64",
			wantURL: "https://github.com/example/confab/releases/download/v0.10.0/confab_0.10.0_linux_amd64.tar.gz",
		},
		{
			name:    "unknown platform",
			goos:    "windows",
			goarch:  "386",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := findAssetURLForPlatform(release, tt.goos, tt.goarch)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tt.wantURL {
				t.Errorf("got %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestFindAssetURLForPlatformDifferentVersions(t *testing.T) {
	// Ensure matching works regardless of version string in the asset name
	release := &githubRelease{
		TagName: "v1.2.3",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{
				Name:               "confab_1.2.3_linux_arm64.tar.gz",
				BrowserDownloadURL: "https://example.com/confab_1.2.3_linux_arm64.tar.gz",
			},
		},
	}

	url, err := findAssetURLForPlatform(release, "linux", "arm64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://example.com/confab_1.2.3_linux_arm64.tar.gz" {
		t.Errorf("got %q", url)
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.1.0", true},
		{"1.0.0", "2.0.0", true},
		{"1.0.1", "1.0.0", false},
		{"1.1.0", "1.0.0", false},
		{"2.0.0", "1.0.0", false},
		{"1.0.0", "1.0.0", false},
		{"dev", "1.0.0", true},
		{"none", "0.0.1", true},
		{"", "0.0.1", true},
		{"0.9.0", "0.10.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.current+"->"+tt.latest, func(t *testing.T) {
			got := isNewerVersion(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("isNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

// makeTarGz creates an in-memory .tar.gz archive containing the given files.
// files is a map of archive path -> content.
func makeTarGz(t *testing.T, files map[string][]byte) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0755,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader(%s): %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("Write(%s): %v", name, err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	return &buf
}

func TestExtractConfabBinary(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\necho hello\n")

	archive := makeTarGz(t, map[string][]byte{
		"confab": binaryContent,
	})

	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "confab")

	if err := extractConfabBinary(archive, destPath); err != nil {
		t.Fatalf("extractConfabBinary: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, binaryContent) {
		t.Errorf("got %q, want %q", got, binaryContent)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Error("binary is not executable")
	}
}

func TestExtractConfabBinaryWithSubdir(t *testing.T) {
	// GoReleaser may nest the binary in a directory
	binaryContent := []byte("binary-v2")

	archive := makeTarGz(t, map[string][]byte{
		"confab_0.10.0_darwin_arm64/confab": binaryContent,
	})

	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "confab")

	if err := extractConfabBinary(archive, destPath); err != nil {
		t.Fatalf("extractConfabBinary: %v", err)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, binaryContent) {
		t.Errorf("got %q, want %q", got, binaryContent)
	}
}

func TestExtractConfabBinaryNotFound(t *testing.T) {
	archive := makeTarGz(t, map[string][]byte{
		"some-other-file": []byte("not the binary"),
	})

	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "confab")

	err := extractConfabBinary(archive, destPath)
	if err == nil {
		t.Fatal("expected error when binary not in archive")
	}
	if got := err.Error(); got != "confab binary not found in archive" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExtractConfabBinaryInvalidGzip(t *testing.T) {
	r := bytes.NewReader([]byte("this is not gzip"))

	err := extractConfabBinary(r, "/tmp/confab-test")
	if err == nil {
		t.Fatal("expected error for invalid gzip data")
	}
}
