package db

import (
	"context"
	"fmt"

	"github.com/agentmesh/backend/internal/models"
)

func (s *Store) GetUserSettings(ctx context.Context, userID string) (models.UserSettings, error) {
	var settings models.UserSettings
	// A missing settings row gets defaults; a missing account remains an error.
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(s.low_balance_usd_micros, $2), s.max_call_spend_usd_micros
		FROM users u LEFT JOIN user_settings s ON s.user_id = u.id
		WHERE u.id = $1
	`, userID, models.DefaultLowBalanceUSDMicros).Scan(
		&settings.LowBalanceUSDMicros, &settings.MaxCallSpendUSDMicros,
	)
	if err != nil {
		return models.UserSettings{}, fmt.Errorf("get user settings: %w", err)
	}
	return settings, nil
}

func (s *Store) UpsertUserSettings(ctx context.Context, userID string, patch models.UserSettingsPatch) (models.UserSettings, error) {
	threshold := models.DefaultLowBalanceUSDMicros
	if patch.LowBalanceUSDMicros != nil {
		threshold = *patch.LowBalanceUSDMicros
	}
	var ceiling *int64
	if patch.SetMaxCallSpend {
		ceiling = patch.MaxCallSpendUSDMicros
	}
	var saved models.UserSettings
	// Merge only supplied fields so concurrent saves cannot overwrite each other.
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_settings (user_id, low_balance_usd_micros, max_call_spend_usd_micros)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			low_balance_usd_micros = CASE WHEN $4 THEN EXCLUDED.low_balance_usd_micros
				ELSE user_settings.low_balance_usd_micros END,
			max_call_spend_usd_micros = CASE WHEN $5 THEN EXCLUDED.max_call_spend_usd_micros
				ELSE user_settings.max_call_spend_usd_micros END,
			updated_at = NOW()
		RETURNING low_balance_usd_micros, max_call_spend_usd_micros
	`, userID, threshold, ceiling, patch.LowBalanceUSDMicros != nil, patch.SetMaxCallSpend).Scan(
		&saved.LowBalanceUSDMicros, &saved.MaxCallSpendUSDMicros,
	)
	if err != nil {
		return models.UserSettings{}, fmt.Errorf("update user settings: %w", err)
	}
	return saved, nil
}

func (s *Store) UpdatePassword(ctx context.Context, userID, previousHash, passwordHash string) (bool, error) {
	// Reauthentication must still be current when the write wins the row lock.
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET password_hash = $3
		WHERE id = $1 AND password_hash = $2 AND password_hash <> ''
	`, userID, previousHash, passwordHash)
	if err != nil {
		return false, fmt.Errorf("update password: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
