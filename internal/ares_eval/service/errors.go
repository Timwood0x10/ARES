package evalapi

import "errors"

var (
	// ErrNilRepository is returned when a nil repository is provided.
	ErrNilRepository = errors.New("eval repository must not be nil")

	// ErrNilServiceConfig is returned when service config is nil.
	ErrNilServiceConfig = errors.New("eval service config must not be nil")

	// ErrEmptySuitePath is returned when suite path is empty.
	ErrEmptySuitePath = errors.New("suite path must not be empty")

	// ErrEmptyAgentConfigs is returned when no agent configs are provided.
	ErrEmptyAgentConfigs = errors.New("at least one agent config is required")

	// ErrInvalidRunID is returned when run_id is empty or malformed.
	ErrInvalidRunID = errors.New("invalid run_id")

	// ErrRunNotFound is returned when a run_id does not exist in storage.
	ErrRunNotFound = errors.New("evaluation run not found")

	// ErrEmptyRunIDs is returned when comparison request has no run IDs.
	ErrEmptyRunIDs = errors.New("at least one run_id is required for comparison")
)
