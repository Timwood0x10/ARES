// Package agents ...
package agents

import "fmt"

var (
	ErrNoResponse  = fmt.Errorf("llm returned empty response")
	ErrParseFailed = fmt.Errorf("output parsing failed")
)
