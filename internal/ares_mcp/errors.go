// Package ares_mcp ...
package ares_mcp

import "fmt"

var (
	ErrDuplicateRegistration = fmt.Errorf("duplicate registration")
	ErrEmptyName             = fmt.Errorf("name must not be empty")
)
