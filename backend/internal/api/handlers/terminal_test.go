package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The terminal is a live root shell on a rented machine. Authentication is
// the whole security boundary, so it is tested before anything else about it.
func TestTerminalRejectsNonOwner(t *testing.T) {
	d, leaseID, _, other := leaseFixture(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.LeaseTerminal(w, withUser(withURLParam(r, "id", leaseID), other))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/leases/"+leaseID+"/terminal", nil)
	if err == nil {
		t.Fatal("a non-owner completed the WebSocket handshake")
	}
}

func TestTerminalRejectsReleasedLease(t *testing.T) {
	d, leaseID, owner, _ := leaseFixture(t)
	if _, err := d.Store.MarkTendrilLeaseReleased(context.Background(), leaseID, 60, 100); err != nil {
		t.Fatalf("MarkTendrilLeaseReleased: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.LeaseTerminal(w, withUser(withURLParam(r, "id", leaseID), owner))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/leases/"+leaseID+"/terminal", nil); err == nil {
		t.Fatal("a released lease accepted a terminal connection")
	}
}
