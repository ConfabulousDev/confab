package daemon

import (
	"testing"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// TestDaemonBinding: an empty ConfigDir collapses to the default binding, and a
// distinct config dir resolves to a non-default binding (kata hpec). This is
// the routing seam that sends a custom-dir session to its own backend.
func TestDaemonBinding(t *testing.T) {
	// Pin the provider's default config dir so the comparison is stable.
	t.Setenv(provider.ClaudeStateDirEnv, t.TempDir())

	d := New(Config{Provider: provider.NameClaudeCode})
	if b := d.binding(); !b.IsDefault {
		t.Errorf("empty ConfigDir: IsDefault=false, want true")
	}

	custom := t.TempDir()
	dc := New(Config{Provider: provider.NameClaudeCode, ConfigDir: custom})
	if b := dc.binding(); b.IsDefault {
		t.Errorf("custom ConfigDir: IsDefault=true, want false")
	}
}
