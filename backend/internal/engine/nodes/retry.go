package nodes

import "errors"

// RetryableError marks an error as safe to retry: whatever failed either
// never reached the outside world, or reached it via an HTTP-idempotent
// method, so a second attempt cannot double an effect that already landed
// once. Node code must opt into this explicitly at the point an error is
// known to be safe — an error left unwrapped defaults to NOT retryable,
// matching the existing fail-loud convention this codebase already uses for
// anything payment-adjacent (see runner.go's criticalAlert and
// ErrSettlementIndeterminate, which both refuse to guess rather than risk a
// double-spend).
type RetryableError struct{ err error }

func (e *RetryableError) Error() string { return e.err.Error() }
func (e *RetryableError) Unwrap() error { return e.err }

// Retryable wraps err so IsRetryable reports true for it. Returns nil
// unchanged so callers can write `return nil, Retryable(err)` without an
// extra nil check.
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return &RetryableError{err: err}
}

// IsRetryable reports whether err, or anything it wraps, was marked
// Retryable at its origin.
func IsRetryable(err error) bool {
	var re *RetryableError
	return errors.As(err, &re)
}
