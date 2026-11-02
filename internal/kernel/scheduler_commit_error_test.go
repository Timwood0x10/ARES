package kernel

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestQuantumRetries_StorageOutcomeIsNotAnExecutorFailure: a completion the store could not confirm is
// a storage outcome. Counting it as a retry inflates the deterministic scorer's retry component and
// attributes a failure to an agent whose execution succeeded.
func TestQuantumRetries_StorageOutcomeIsNotAnExecutorFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"executor failure still counts", errors.New("tool exploded"), 1},
		{"an unknown commit does not", taskfabric.ErrCommitUnknown, 0},
		{"a refused commit does not", taskfabric.ErrCommitRefused, 0},
		{"wrapping keeps the class", errors.Join(errors.New("ctx"), taskfabric.ErrCommitRefused), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, quantumRetries(tc.err))
		})
	}
}
