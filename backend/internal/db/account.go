package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrActiveMachineLeases = errors.New("active machine leases block account deletion")
	ErrAccountChanged      = errors.New("account credentials changed")
)

func (s *Store) UserExists(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check account: %w", err)
	}
	return exists, nil
}

func (s *Store) DeleteAccount(ctx context.Context, userID, passwordHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin account deletion: %w", err)
	}
	defer tx.Rollback(context.Background())

	// The row lock also blocks new leases and owned records through their foreign keys.
	var currentHash, email string
	if err := tx.QueryRow(ctx, `SELECT password_hash, email FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&currentHash, &email); err != nil {
		return fmt.Errorf("lock account: %w", err)
	}
	if currentHash != passwordHash {
		return ErrAccountChanged
	}

	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM tendril_leases WHERE user_id = $1 AND status = 'active'
	)`, userID).Scan(&active); err != nil {
		return fmt.Errorf("check machine leases: %w", err)
	}
	if active {
		return ErrActiveMachineLeases
	}
	if _, err := tx.Exec(ctx, `DELETE FROM tendril_leases WHERE user_id = $1 AND status <> 'active'`, userID); err != nil {
		return fmt.Errorf("delete machine lease history: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM waitlist_archived WHERE lower(email) = lower($1)`, email); err != nil {
		return fmt.Errorf("delete account waitlist entry: %w", err)
	}
	// Remaining owned records cascade; unrelated public settlement records have no owner.
	result, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if result.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit account deletion: %w", err)
	}
	return nil
}
