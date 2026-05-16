package cmd

import (
	"bytes"
	"testing"
)

func TestWriteClaudeHookResponseJSON(t *testing.T) {
	var out bytes.Buffer

	writeClaudeHookResponse(&out, false)

	want := "{\"continue\":true,\"stopReason\":\"\",\"suppressOutput\":false}\n"
	if out.String() != want {
		t.Fatalf("response JSON = %q, want %q", out.String(), want)
	}
}
