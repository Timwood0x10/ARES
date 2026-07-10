// Package main tests for the arena CLI command.
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrintUsage(t *testing.T) {
	// printUsage should not panic.
	printUsage()
}

func TestSeparateArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantFlags      []string
		wantPositional []string
	}{
		{
			name:           "empty",
			args:           nil,
			wantFlags:      nil,
			wantPositional: nil,
		},
		{
			name:           "only positional",
			args:           []string{"run", "scenario.yaml"},
			wantFlags:      nil,
			wantPositional: []string{"run", "scenario.yaml"},
		},
		{
			name:           "flag with value",
			args:           []string{"--addr", "localhost:8080", "serve"},
			wantFlags:      []string{"--addr", "localhost:8080"},
			wantPositional: []string{"serve"},
		},
		{
			name:           "flag with equals",
			args:           []string{"--addr=:8080", "serve"},
			wantFlags:      []string{"--addr=:8080", "serve"},
			wantPositional: nil,
		},
		{
			name:           "flags only",
			args:           []string{"--help"},
			wantFlags:      []string{"--help"},
			wantPositional: nil,
		},
		{
			name:           "positional after flag with value",
			args:           []string{"--timeout", "30", "run", "scenario.yaml"},
			wantFlags:      []string{"--timeout", "30"},
			wantPositional: []string{"run", "scenario.yaml"},
		},
		{
			name:           "short flag consumes next arg",
			args:           []string{"-v", "run"},
			wantFlags:      []string{"-v", "run"},
			wantPositional: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, positional := separateArgs(tt.args)
			assert.Equal(t, tt.wantFlags, flags, "flags mismatch")
			assert.Equal(t, tt.wantPositional, positional, "positional mismatch")
		})
	}
}
