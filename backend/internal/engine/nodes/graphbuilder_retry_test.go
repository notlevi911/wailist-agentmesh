package nodes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildModelRetryPolicy(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		budget time.Duration
		calls  int32
	}{
		{"invalid request", 400, 10 * time.Second, 1},
		{"missing credentials", 401, 10 * time.Second, 1},
		{"refused credentials", 403, 10 * time.Second, 1},
		{"rate limited", 429, 10 * time.Second, 2},
		{"service unavailable", 503, 10 * time.Second, 2},
		{"insufficient retry budget", 503, 300 * time.Millisecond, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
				io.WriteString(w, `{"error":{"message":"rejected"}}`)
			}))
			defer srv.Close()
			SetGeminiBaseURL(srv.URL)
			defer SetGeminiBaseURL("https://generativelanguage.googleapis.com")
			_, err := BuildGraph(context.Background(), BuildRequest{APIKey: "k", Message: "hello", TimeBudget: tt.budget})
			if err == nil {
				t.Fatal("a failed call with no graph changes must remain an error")
			}
			if got := calls.Load(); got != tt.calls {
				t.Fatalf("got %d requests, want %d", got, tt.calls)
			}
		})
	}
}

func TestBuildModelCancellationInterruptsRetryWait(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	_, err := callBuildModel(ctx, srv.URL, nil, map[string]any{}, func() time.Duration {
		// The first request has settled; cancel while the retry wait begins.
		cancel()
		return 10 * time.Second
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
	if calls.Load() != 1 || time.Since(started) >= modelRetryDelay {
		t.Fatalf("cancellation must stop the wait and suppress retry, calls=%d", calls.Load())
	}
}

func TestBuildModelCancellationKeepsPartialGraph(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			io.WriteString(w, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"add_node","args":{"type":"trigger","template":"manual","name":"Start"}}}]}}]}`)
			return
		}
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	SetGeminiBaseURL(srv.URL)
	defer SetGeminiBaseURL("https://generativelanguage.googleapis.com")
	result, err := BuildGraph(ctx, BuildRequest{APIKey: "k", Message: "build", TimeBudget: 10 * time.Second})
	if err != nil || len(result.Graph.Nodes) != 1 || result.Reply == "" {
		t.Fatalf("partial work must survive cancellation: nodes=%d reply=%q err=%v", len(result.Graph.Nodes), result.Reply, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("cancellation must not send another request, got %d", calls.Load())
	}
}
