package leader

import (
	"testing"

	apperrors "goagentx/internal/core/errors"
	"goagentx/internal/core/models"
	"goagentx/internal/llm/output"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestProfileParser creates a profileParser with nil LLM adapter for testing.
func newTestProfileParser() *profileParser {
	return &profileParser{
		llmAdapter: nil,
		template:   output.NewTemplateEngine(),
		promptTpl:  "{{.input}}",
		validator:  nil,
		maxRetries: 3,
	}
}

func TestGetDefaultProfile_ReturnsEmptyPreferences(t *testing.T) {
	parser := newTestProfileParser()

	profile := parser.getDefaultProfile()

	require.NotNil(t, profile, "getDefaultProfile() should not return nil")
	assert.NotNil(t, profile.Preferences, "Preferences should not be nil")
	assert.Empty(t, profile.Style, "Style should be empty (no hardcoded StyleCasual)")
	assert.Empty(t, profile.Preferences, "Preferences should be an empty map")
}

func TestValidateProfile_WithPreferences_Passes(t *testing.T) {
	parser := newTestProfileParser()

	profile := &models.UserProfile{
		Preferences: map[string]any{
			"destination": "Tokyo",
			"budget":      5000,
		},
	}

	err := parser.validateProfile(profile)
	assert.NoError(t, err, "validateProfile() should return nil when Preferences is populated")
}

func TestValidateProfile_WithStyle_Passes(t *testing.T) {
	parser := newTestProfileParser()

	// Profile with Style but no Preferences - backward compatibility
	profile := &models.UserProfile{
		Style: []models.StyleTag{models.StyleTag("casual"), models.StyleTag("minimalist")},
	}

	err := parser.validateProfile(profile)
	assert.NoError(t, err, "validateProfile() should return nil when Style is populated (backward compatible)")
}

func TestValidateProfile_EmptyProfile_Fails(t *testing.T) {
	parser := newTestProfileParser()

	// Profile with both Preferences and Style empty
	profile := &models.UserProfile{
		Preferences: map[string]any{},
		Style:       nil,
	}

	err := parser.validateProfile(profile)
	assert.ErrorIs(t, err, apperrors.ErrProfileValidationFailed,
		"validateProfile() should return ErrProfileValidationFailed for empty profile")
}

func TestValidateProfile_NilProfile_Fails(t *testing.T) {
	parser := newTestProfileParser()

	err := parser.validateProfile(nil)
	assert.ErrorIs(t, err, apperrors.ErrNilPointer,
		"validateProfile() should return ErrNilPointer for nil profile")
}
