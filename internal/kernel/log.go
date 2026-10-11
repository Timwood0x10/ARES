package kernel

import (
	"github.com/Timwood0x10/ares/internal/logger"
)

// log is the module-scoped structured logger for the kernel scheduler.
// Production code in this package must use this instead of the standard
// library log package (library code does not print
// directly; output goes through an injected logger or event sink).
//
// logger.New (not logger.Module) so every entry carries the METHOD that
// produced it as well as the module: a scheduler decision is only actionable
// when the line says which stage took it (admissible / executeWithCandidates /
// handleStaleWinner), and a module-only logger cannot answer that.
var log = logger.New("kernel")
