package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

// RecommendRepository handles recommendation persistence.
type RecommendRepository struct {
	db DBTX
}

// NewRecommendRepository creates a new RecommendRepository.
func NewRecommendRepository(pool *Pool) *RecommendRepository {
	return &RecommendRepository{db: pool.db}
}

// NewRecommendRepositoryWithDB creates a new RecommendRepository with a transaction or connection.
func NewRecommendRepositoryWithDB(db DBTX) *RecommendRepository {
	return &RecommendRepository{db: db}
}

// Create creates a new recommendation result.
func (r *RecommendRepository) Create(ctx context.Context, result *models.RecommendResult) error {
	itemsJSON, err := json.Marshal(result.Items)
	if err != nil {
		return errors.Wrap(err, "marshal items")
	}

	feedbackJSON, err := json.Marshal(result.Feedback)
	if err != nil {
		return errors.Wrap(err, "marshal feedback")
	}

	metadataJSON, err := json.Marshal(result.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	query := `
		INSERT INTO recommendations (session_id, user_id, items, reason, total_price, match_score, occasion, season, feedback, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err = r.db.ExecContext(ctx, query,
		result.SessionID,
		result.UserID,
		itemsJSON,
		result.Reason,
		result.TotalPrice,
		result.MatchScore,
		result.Occasion,
		result.Season,
		feedbackJSON,
		metadataJSON,
		result.CreatedAt,
	)
	if err != nil {
		return errors.Wrap(err, "insert recommendation")
	}

	return nil
}

// GetBySessionID retrieves a recommendation by session ID.
func (r *RecommendRepository) GetBySessionID(ctx context.Context, sessionID string) (*models.RecommendResult, error) {
	query := `
		SELECT session_id, user_id, items, reason, total_price, match_score, occasion, season, feedback, metadata, created_at
		FROM recommendations WHERE session_id = $1
	`

	var result models.RecommendResult
	var itemsJSON, feedbackJSON, metadataJSON []byte

	err := r.db.QueryRowContext(ctx, query, sessionID).Scan(
		&result.SessionID,
		&result.UserID,
		&itemsJSON,
		&result.Reason,
		&result.TotalPrice,
		&result.MatchScore,
		&result.Occasion,
		&result.Season,
		&feedbackJSON,
		&metadataJSON,
		&result.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, errors.ErrRecordNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "query recommendation")
	}

	if err := json.Unmarshal(itemsJSON, &result.Items); err != nil {
		return nil, errors.Wrap(err, "unmarshal items")
	}
	if err := json.Unmarshal(feedbackJSON, &result.Feedback); err != nil {
		return nil, errors.Wrap(err, "unmarshal feedback")
	}
	if err := json.Unmarshal(metadataJSON, &result.Metadata); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata")
	}

	return &result, nil
}

// UpdateFeedback updates user feedback for a recommendation.
func (r *RecommendRepository) UpdateFeedback(ctx context.Context, sessionID string, feedback *models.UserFeedback) error {
	feedbackJSON, err := json.Marshal(feedback)
	if err != nil {
		return errors.Wrap(err, "marshal feedback")
	}

	query := `UPDATE recommendations SET feedback = $1 WHERE session_id = $2`

	result, err := r.db.ExecContext(ctx, query, feedbackJSON, sessionID)
	if err != nil {
		return errors.Wrap(err, "update feedback")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "rows affected")
	}
	if rowsAffected == 0 {
		return errors.ErrRecordNotFound
	}

	return nil
}

// ListByUserID lists recommendations by user ID.
func (r *RecommendRepository) ListByUserID(ctx context.Context, userID string, limit, offset int) ([]*models.RecommendResult, error) {
	query := `
		SELECT session_id, user_id, items, reason, total_price, match_score, occasion, season, feedback, metadata, created_at
		FROM recommendations WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, errors.Wrap(err, "query recommendations")
	}
	defer rows.Close()
	// nolint: errcheck // Rows are closed by defer
	var results []*models.RecommendResult
	for rows.Next() {
		var result models.RecommendResult
		var itemsJSON, feedbackJSON, metadataJSON []byte

		if err := rows.Scan(
			&result.SessionID,
			&result.UserID,
			&itemsJSON,
			&result.Reason,
			&result.TotalPrice,
			&result.MatchScore,
			&result.Occasion,
			&result.Season,
			&feedbackJSON,
			&metadataJSON,
			&result.CreatedAt,
		); err != nil {
			return nil, errors.Wrap(err, "scan recommendation")
		}

		if err := json.Unmarshal(itemsJSON, &result.Items); err != nil {
			return nil, errors.Wrap(err, "unmarshal items")
		}
		if err := json.Unmarshal(feedbackJSON, &result.Feedback); err != nil {
			return nil, errors.Wrap(err, "unmarshal feedback")
		}
		if err := json.Unmarshal(metadataJSON, &result.Metadata); err != nil {
			return nil, errors.Wrap(err, "unmarshal metadata")
		}

		results = append(results, &result)
	}

	if err := rows.Err(); err != nil {
		log.Error("Failed to iterate recommendations", "error", err)
		return nil, errors.Wrap(err, "iterate recommendations")
	}

	return results, nil
}

// Delete deletes a recommendation.
func (r *RecommendRepository) Delete(ctx context.Context, sessionID string) error {
	query := `DELETE FROM recommendations WHERE session_id = $1`

	result, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return errors.Wrap(err, "delete recommendation")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "rows affected")
	}
	if rowsAffected == 0 {
		return errors.ErrRecordNotFound
	}

	return nil
}
