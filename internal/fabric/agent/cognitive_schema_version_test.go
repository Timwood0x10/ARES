package agentfabric

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecodeCognitiveState_RefusesANewerSchema pins the P2-6③ (A4) behaviour AND its
// operator-facing message.
//
// The refusal itself is right — a newer build's format cannot be interpreted — but the
// deployment consequence is what a reader of the revival log needs: during a rolling
// upgrade this is not corruption, it is an old node meeting a new node's snapshot, and the
// in-place revival degrades to a replacement. The message must say so; "schema version
// mismatch" alone reads like a bug and invites the wrong reaction (retrying, or treating the
// state as damaged).
func TestDecodeCognitiveState_RefusesANewerSchema(t *testing.T) {
	_, err := DecodeCognitiveState(map[string]any{
		"schema_version": CognitiveStateSchemaVersion + 1,
		"context":        "written by a newer build",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCognitiveStateSchemaVersion),
		"callers key on the sentinel, so the wrapped identity must survive: %v", err)
	assert.Contains(t, err.Error(), "newer build",
		"the message must name the rolling-upgrade cause, not just the mismatch")
	assert.Contains(t, err.Error(), "CognitiveStateSchemaVersion",
		"and point at the rule that governs it")
}

// TestDecodeCognitiveState_AcceptsCurrentAndLegacy: the refusal must not have made the
// ordinary cases stricter — the current version and the legacy zero version still decode.
func TestDecodeCognitiveState_AcceptsCurrentAndLegacy(t *testing.T) {
	for name, state := range map[string]map[string]any{
		"current":     {"schema_version": CognitiveStateSchemaVersion, "context": "current"},
		"legacy zero": {"context": "no version stamped"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeCognitiveState(state)
			require.NoError(t, err)
			assert.NotEmpty(t, got.Context)
		})
	}
}
