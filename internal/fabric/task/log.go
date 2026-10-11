package taskfabric

import (
	"github.com/Timwood0x10/ares/internal/logger"
)

// log is the module-scoped structured logger for the task fabric.
// Production code in this package must use this instead of the standard
// library log package (library code does not print
// directly; output goes through an injected logger or event sink).
//
// logger.New (not logger.Module) so every entry carries the METHOD that
// produced it as well as the module: a durable-write or append-ordering
// failure is only triageable when the line names the transition that hit it
// (flushAppends / recordWithExtrasLocked / RestoreFromStore).
var log = logger.New("taskfabric")
