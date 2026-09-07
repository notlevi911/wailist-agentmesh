package engine

import (
	"context"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
)

type reaperStore struct {
	expired  []models.TendrilLease
	released []string
}

func (s *reaperStore) ListExpiredTendrilLeases(_ context.Context, _ time.Time) ([]models.TendrilLease, error) {
	return s.expired, nil
}
func (s *reaperStore) MarkTendrilLeaseReleased(_ context.Context, id string, _, _ int64) (bool, error) {
	s.released = append(s.released, id)
	return true, nil
}

// An expired lease is a meter still running against the shared pool. One
// failing release must not stop the others from being reaped.
func TestReapExpiredLeasesContinuesPastFailures(t *testing.T) {
	store := &reaperStore{expired: []models.TendrilLease{
		{ID: "bad", LeaseID: "l1", LeaseTokenEnc: "not-decryptable"},
		{ID: "good", LeaseID: "l2", LeaseTokenEnc: "not-decryptable"},
	}}
	r := &Runner{}
	released, err := r.reapWith(context.Background(), store, func(_ context.Context, l models.TendrilLease) error {
		if l.ID == "bad" {
			return context.DeadlineExceeded
		}
		store.released = append(store.released, l.ID)
		return nil
	})
	if err != nil {
		t.Fatalf("reapWith: %v", err)
	}
	if released != 1 {
		t.Errorf("released = %d, want 1", released)
	}
	if len(store.released) != 1 || store.released[0] != "good" {
		t.Errorf("released ids = %v, want [good]", store.released)
	}
}
