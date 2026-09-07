package engine

import (
	"context"
	"log"
	"time"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

type expiredLeaseLister interface {
	ListExpiredTendrilLeases(ctx context.Context, now time.Time) ([]models.TendrilLease, error)
}

// reapWith is the testable core: list what has expired, release each one, and
// keep going past individual failures. A single unreachable machine must not
// leave every other meter running.
func (r *Runner) reapWith(ctx context.Context, lister expiredLeaseLister, release func(context.Context, models.TendrilLease) error) (int, error) {
	expired, err := lister.ListExpiredTendrilLeases(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	released := 0
	for _, lease := range expired {
		if err := release(ctx, lease); err != nil {
			log.Printf("tendril reaper: lease %s: %v", lease.LeaseID, err)
			continue
		}
		released++
	}
	return released, nil
}

// ReapExpiredLeases releases every lease whose funded window has closed.
func (r *Runner) ReapExpiredLeases(ctx context.Context) (int, error) {
	if r.tendrilClient == nil {
		return 0, nil
	}
	return r.reapWith(ctx, r.store, func(ctx context.Context, lease models.TendrilLease) error {
		_, _, err := nodes.ReleaseLease(ctx, nodes.TendrilConfig{
			Client: r.tendrilClient, Store: r.store, EncryptKey: r.encryptionKey,
		}, lease)
		return err
	})
}

// StartLeaseReaper runs ReapExpiredLeases forever. Tendril has its own
// watchdog, but relying on it would mean AgentMesh's own rows drift out of
// sync with what the user is actually being charged for.
func (r *Runner) StartLeaseReaper(ctx context.Context, every time.Duration) {
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := r.ReapExpiredLeases(ctx); err != nil {
					log.Printf("tendril reaper: %v", err)
				} else if n > 0 {
					log.Printf("tendril reaper: released %d expired lease(s)", n)
				}
			}
		}
	}()
}
