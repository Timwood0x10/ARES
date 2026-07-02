// Package mutation ...
package mutation

import "fmt"

var (
	ErrNilParent    = fmt.Errorf("parent strategy must not be nil")
	ErrInvalidCount = fmt.Errorf("mutation count must be positive")
)
