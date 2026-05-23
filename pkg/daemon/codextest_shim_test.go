package daemon

import (
	"testing"

	"github.com/ConfabulousDev/confab/pkg/codextest"
)

// codextestFixtureShim wraps codextest.Fixture with a daemon-test-friendly
// API: terse addRoot/addChild methods that return plain structs, so test
// bodies stay readable without depending on the full Fixture surface.
type codextestFixtureShim struct {
	*codextest.Fixture
}

// codexShimEntry is the minimal info daemon tests need from a fixture
// rollout: the thread UUID + the absolute rollout-file path. Exposing only
// these keeps the tests focused on assertions, not fixture mechanics.
type codexShimEntry struct {
	threadUUID  string
	rolloutPath string
}

func newCodexFixtureShim(t *testing.T) *codextestFixtureShim {
	t.Helper()
	return &codextestFixtureShim{Fixture: codextest.NewFixture(t)}
}

// addRoot inserts a root thread with a user message and returns its
// thread UUID + rollout path.
func (s *codextestFixtureShim) addRoot(uuid, firstUserMsg string) codexShimEntry {
	b := s.AddRoot(uuid).WithSessionMeta("/work", "gpt-5")
	if firstUserMsg != "" {
		b.WithUserMessage(firstUserMsg)
	}
	return codexShimEntry{threadUUID: b.ThreadUUID(), rolloutPath: b.Path()}
}

// addChild inserts a subagent thread with the given parent UUID and
// agent_role, plus a user message line. The fixture creates the rollout
// JSONL file on disk so the engine's per-cycle provider descendant discovery
// validates the path.
func (s *codextestFixtureShim) addChild(parentUUID, uuid, firstUserMsg, agentRole string) codexShimEntry {
	b := s.AddSubagent(parentUUID, uuid, codextest.SubagentOpts{AgentRole: agentRole}).
		WithSessionMeta("/work", "gpt-5")
	if firstUserMsg != "" {
		b.WithUserMessage(firstUserMsg)
	}
	return codexShimEntry{threadUUID: b.ThreadUUID(), rolloutPath: b.Path()}
}
