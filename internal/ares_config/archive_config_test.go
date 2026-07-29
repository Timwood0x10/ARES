// Package ares_config — archive config tests.
//
// Verifies the ArchiveConfig default-on semantics (*bool Enabled), that
// setDefaults fills Dir/MaxRounds unconditionally, and that validateMemory
// enforces Dir/MaxRounds only when archiving is active.
package ares_config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Potential bug scenarios tested below:
//  1. Enabled *bool nil treated as enabled (default-on). A nil pointer must
//     not be dereferenced. Covered by TestArchiveConfig_IsEnabled.
//  2. setDefaults not filling Dir/MaxRounds — operators who skip defaults
//     would get an empty Dir and MaxRounds=0, breaking the archive writer.
//     Covered by TestArchiveConfig_DefaultsApplied.
//  3. Validation firing when archive is disabled — an operator who sets
//     enabled: false should not be forced to set Dir/MaxRounds. Covered by
//     TestArchiveConfig_ValidationDisabledSkipsFields.

func TestArchiveConfig_IsEnabled(t *testing.T) {
	t.Run("nil_pointer_is_enabled", func(t *testing.T) {
		cfg := ArchiveConfig{Enabled: nil}
		assert.True(t, cfg.IsEnabled(), "nil Enabled must default to enabled")
	})

	t.Run("true_pointer_is_enabled", func(t *testing.T) {
		b := true
		cfg := ArchiveConfig{Enabled: &b}
		assert.True(t, cfg.IsEnabled())
	})

	t.Run("false_pointer_is_disabled", func(t *testing.T) {
		b := false
		cfg := ArchiveConfig{Enabled: &b}
		assert.False(t, cfg.IsEnabled())
	})
}

func TestArchiveConfig_DefaultsApplied(t *testing.T) {
	t.Run("empty_archive_gets_defaults", func(t *testing.T) {
		cfg := &Config{Memory: MemoryConfig{Archive: ArchiveConfig{}}}
		cfg.setDefaults()
		assert.Equal(t, ".context/rounds", cfg.Memory.Archive.Dir)
		assert.Equal(t, 200, cfg.Memory.Archive.MaxRounds)
	})

	t.Run("explicit_values_preserved", func(t *testing.T) {
		cfg := &Config{Memory: MemoryConfig{Archive: ArchiveConfig{
			Dir:       "/data/rounds",
			MaxRounds: 50,
		}}}
		cfg.setDefaults()
		assert.Equal(t, "/data/rounds", cfg.Memory.Archive.Dir)
		assert.Equal(t, 50, cfg.Memory.Archive.MaxRounds)
	})

	t.Run("defaults_applied_even_when_disabled", func(t *testing.T) {
		// Dir/MaxRounds defaults apply regardless of Enabled so the values
		// are always valid if the operator later flips Enabled on.
		b := false
		cfg := &Config{Memory: MemoryConfig{Archive: ArchiveConfig{Enabled: &b}}}
		cfg.setDefaults()
		assert.Equal(t, ".context/rounds", cfg.Memory.Archive.Dir)
		assert.Equal(t, 200, cfg.Memory.Archive.MaxRounds)
	})
}

func TestArchiveConfig_Validation(t *testing.T) {
	t.Run("enabled_empty_dir_errors", func(t *testing.T) {
		// Bypass setDefaults so Dir stays empty, then validate.
		cfg := &Config{Memory: MemoryConfig{Archive: ArchiveConfig{
			MaxRounds: 200,
		}}}
		err := cfg.validateMemory()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "archive dir must be non-empty")
	})

	t.Run("enabled_zero_max_rounds_errors", func(t *testing.T) {
		cfg := &Config{Memory: MemoryConfig{Archive: ArchiveConfig{
			Dir:       ".context/rounds",
			MaxRounds: 0,
		}}}
		err := cfg.validateMemory()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid archive max_rounds")
	})

	t.Run("enabled_negative_max_rounds_errors", func(t *testing.T) {
		cfg := &Config{Memory: MemoryConfig{Archive: ArchiveConfig{
			Dir:       ".context/rounds",
			MaxRounds: -5,
		}}}
		err := cfg.validateMemory()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid archive max_rounds")
	})

	t.Run("disabled_skips_field_validation", func(t *testing.T) {
		// When disabled, empty Dir / zero MaxRounds must NOT cause an error.
		b := false
		cfg := &Config{Memory: MemoryConfig{Archive: ArchiveConfig{Enabled: &b}}}
		err := cfg.validateMemory()
		require.NoError(t, err, "disabled archive must not enforce Dir/MaxRounds")
	})

	t.Run("enabled_valid_config_passes", func(t *testing.T) {
		cfg := &Config{Memory: MemoryConfig{Archive: ArchiveConfig{
			Dir:       ".context/rounds",
			MaxRounds: 200,
		}}}
		err := cfg.validateMemory()
		require.NoError(t, err)
	})
}
