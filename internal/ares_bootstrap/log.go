// Package ares_bootstrap ...
package ares_bootstrap

import "github.com/Timwood0x10/ares/internal/logger"

// log is the package-level structured logger.
var log = logger.Module("ares_bootstrap")

// logEvolution is the evolution subsystem's module-scoped logger. The module name is carried by
// the logger (logger.Module attaches it), never passed as an attribute: passing "module" again
// produces a line with two module keys, which is worse than none.
var logEvolution = logger.Module("ares_bootstrap.evolution")
