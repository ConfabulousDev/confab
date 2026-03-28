// ABOUTME: Tests for the confab retro command.
// ABOUTME: Validates --output-dir file writing (JSON + transcript extraction) and directory creation.
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRetro_StdoutOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"metadata":   map[string]interface{}{"session_id": "uuid-123"},
			"transcript": "<transcript/>",
		})
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgContent := `{"backend_url":"` + server.URL + `","api_key":"test-key"}`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)

	// Empty outputDir means stdout only — no files should be written
	if err := runRetro("uuid-123", 0, ""); err != nil {
		t.Fatalf("runRetro() error = %v", err)
	}
}

func TestRunRetro_OutputDir(t *testing.T) {
	transcriptXML := "<transcript>\n<user>Hello</user>\n</transcript>"
	backendResp := map[string]interface{}{
		"metadata": map[string]interface{}{
			"session_id":  "uuid-123",
			"external_id": "ext-456",
			"title":       "Test Session",
		},
		"transcript": transcriptXML,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(backendResp)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "retro-out")

	// Write a config file so EnsureAuthenticated succeeds
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgContent := `{"backend_url":"` + server.URL + `","api_key":"test-key"}`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)

	err := runRetro("uuid-123", 0, outputDir)
	if err != nil {
		t.Fatalf("runRetro() error = %v", err)
	}

	// Check response.json was written
	jsonPath := filepath.Join(outputDir, "response.json")
	jsonContent, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("Failed to read response.json: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(jsonContent, &parsed); err != nil {
		t.Fatalf("response.json is not valid JSON: %v", err)
	}
	if _, ok := parsed["metadata"]; !ok {
		t.Error("response.json missing 'metadata' field")
	}

	// Check transcript.xml was written
	xmlPath := filepath.Join(outputDir, "transcript.xml")
	xmlContent, err := os.ReadFile(xmlPath)
	if err != nil {
		t.Fatalf("Failed to read transcript.xml: %v", err)
	}

	if string(xmlContent) != transcriptXML {
		t.Errorf("transcript.xml = %q, want %q", string(xmlContent), transcriptXML)
	}
}

func TestRunRetro_OutputDir_CreatesDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"metadata":   map[string]interface{}{"session_id": "id"},
			"transcript": "<t/>",
		})
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "nested", "deep", "retro-out")

	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgContent := `{"backend_url":"` + server.URL + `","api_key":"test-key"}`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)

	err := runRetro("id", 0, outputDir)
	if err != nil {
		t.Fatalf("runRetro() error = %v", err)
	}

	// Both files should exist in the nested directory
	if _, err := os.Stat(filepath.Join(outputDir, "response.json")); err != nil {
		t.Errorf("response.json not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "transcript.xml")); err != nil {
		t.Errorf("transcript.xml not created: %v", err)
	}
}
