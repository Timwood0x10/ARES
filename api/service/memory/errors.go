// Package memory — memory service errors.
package memory

import "errors"

var errNotImplemented = errors.New("memory: not implemented — requires bootstrap-level wiring")
