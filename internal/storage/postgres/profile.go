package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	coreerrors "github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/errors"
)

// ProfileRepository handles user profile persistence.
type ProfileRepository struct {
	db DBTX
}

// NewProfileRepository creates a new ProfileRepository.
func NewProfileRepository(pool *Pool) *ProfileRepository {
	return &ProfileRepository{db: pool.db}
}

// NewProfileRepositoryWithDB creates a new ProfileRepository with a transaction or connection.
func NewProfileRepositoryWithDB(db DBTX) *ProfileRepository {
	return &ProfileRepository{db: db}
}

// Create creates a new user profile.
func (r *ProfileRepository) Create(ctx context.Context, profile *models.UserProfile) error {
	styleJSON, err := json.Marshal(profile.Style)
	if err != nil {
		return errors.Wrap(err, "marshal style")
	}

	budgetJSON, err := json.Marshal(profile.Budget)
	if err != nil {
		return errors.Wrap(err, "marshal budget")
	}

	colorsJSON, err := json.Marshal(profile.Colors)
	if err != nil {
		return errors.Wrap(err, "marshal colors")
	}

	occasionsJSON, err := json.Marshal(profile.Occasions)
	if err != nil {
		return errors.Wrap(err, "marshal occasions")
	}

	preferencesJSON, err := json.Marshal(profile.Preferences)
	if err != nil {
		return errors.Wrap(err, "marshal preferences")
	}

	query := `
		INSERT INTO user_profiles (user_id, name, gender, age, occupation, style, budget, colors, occasions, body_type, preferences, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err = r.db.ExecContext(ctx, query,
		profile.UserID,
		profile.Name,
		profile.Gender,
		profile.Age,
		profile.Occupation,
		styleJSON,
		budgetJSON,
		colorsJSON,
		occasionsJSON,
		profile.BodyType,
		preferencesJSON,
		profile.CreatedAt,
		profile.UpdatedAt,
	)
	if err != nil {
		return errors.Wrap(err, "insert profile")
	}

	return nil
}

// GetByID retrieves a user profile by ID.
func (r *ProfileRepository) GetByID(ctx context.Context, userID string) (*models.UserProfile, error) {
	query := `
		SELECT user_id, name, gender, age, occupation, style, budget, colors, occasions, body_type, preferences, created_at, updated_at
		FROM user_profiles WHERE user_id = $1
	`

	var profile models.UserProfile
	var styleJSON, budgetJSON, colorsJSON, occasionsJSON, preferencesJSON []byte

	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&profile.UserID,
		&profile.Name,
		&profile.Gender,
		&profile.Age,
		&profile.Occupation,
		&styleJSON,
		&budgetJSON,
		&colorsJSON,
		&occasionsJSON,
		&profile.BodyType,
		&preferencesJSON,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, coreerrors.ErrRecordNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "query profile")
	}

	if err := json.Unmarshal(styleJSON, &profile.Style); err != nil {
		return nil, errors.Wrap(err, "unmarshal style")
	}
	if err := json.Unmarshal(budgetJSON, &profile.Budget); err != nil {
		return nil, errors.Wrap(err, "unmarshal budget")
	}
	if err := json.Unmarshal(colorsJSON, &profile.Colors); err != nil {
		return nil, errors.Wrap(err, "unmarshal colors")
	}
	if err := json.Unmarshal(occasionsJSON, &profile.Occasions); err != nil {
		return nil, errors.Wrap(err, "unmarshal occasions")
	}
	if err := json.Unmarshal(preferencesJSON, &profile.Preferences); err != nil {
		return nil, errors.Wrap(err, "unmarshal preferences")
	}

	return &profile, nil
}

// Update updates a user profile.
func (r *ProfileRepository) Update(ctx context.Context, profile *models.UserProfile) error {
	profile.UpdatedAt = time.Now()

	styleJSON, err := json.Marshal(profile.Style)
	if err != nil {
		return errors.Wrap(err, "marshal style")
	}

	budgetJSON, err := json.Marshal(profile.Budget)
	if err != nil {
		return errors.Wrap(err, "marshal budget")
	}

	colorsJSON, err := json.Marshal(profile.Colors)
	if err != nil {
		return errors.Wrap(err, "marshal colors")
	}

	occasionsJSON, err := json.Marshal(profile.Occasions)
	if err != nil {
		return errors.Wrap(err, "marshal occasions")
	}

	preferencesJSON, err := json.Marshal(profile.Preferences)
	if err != nil {
		return errors.Wrap(err, "marshal preferences")
	}

	query := `
		UPDATE user_profiles
		SET name = $1, gender = $2, age = $3, occupation = $4, style = $5, budget = $6,
		    colors = $7, occasions = $8, body_type = $9, preferences = $10, updated_at = $11
		WHERE user_id = $12
	`

	result, err := r.db.ExecContext(ctx, query,
		profile.Name,
		profile.Gender,
		profile.Age,
		profile.Occupation,
		styleJSON,
		budgetJSON,
		colorsJSON,
		occasionsJSON,
		profile.BodyType,
		preferencesJSON,
		profile.UpdatedAt,
		profile.UserID,
	)
	if err != nil {
		return errors.Wrap(err, "update profile")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "rows affected")
	}
	if rowsAffected == 0 {
		return coreerrors.ErrRecordNotFound
	}

	return nil
}

// Delete deletes a user profile.
func (r *ProfileRepository) Delete(ctx context.Context, userID string) error {
	query := `DELETE FROM user_profiles WHERE user_id = $1`

	result, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return errors.Wrap(err, "delete profile")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "rows affected")
	}
	if rowsAffected == 0 {
		return coreerrors.ErrRecordNotFound
	}

	return nil
}

// Exists checks if a profile exists.
func (r *ProfileRepository) Exists(ctx context.Context, userID string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM user_profiles WHERE user_id = $1)`

	var exists bool
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&exists)
	if err != nil {
		return false, errors.Wrap(err, "check exists")
	}

	return exists, nil
}
