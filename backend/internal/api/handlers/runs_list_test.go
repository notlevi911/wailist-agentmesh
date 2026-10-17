package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/testutil"
)

type runPageBody struct {
	Runs       []models.RunSummary `json:"runs"`
	NextCursor *string             `json:"nextCursor"`
}

func runsListRequest(path, userID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, userID))
}

func runsListOwner() string {
	return fmt.Sprintf("runs-list-owner-%d", time.Now().UnixNano())
}

// runsListWorkflowWithRuns creates a workflow owned by userID with n runs.
func runsListWorkflowWithRuns(t *testing.T, d *handlers.Deps, userID string, n int) string {
	t.Helper()
	testutil.EnsureUsers(t, userID)
	ctx := context.Background()
	wf, err := d.Store.CreateWorkflow(ctx, "Runs List Handler", userID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Store.DeleteWorkflow(context.Background(), wf.ID) })
	for i := 0; i < n; i++ {
		if _, err := d.Store.CreateRun(ctx, wf.ID, "manual", []byte(`{"secret":"do-not-list"}`)); err != nil {
			t.Fatal(err)
		}
	}
	return wf.ID
}

func serveWorkflowRuns(d *handlers.Deps, workflowID, userID, query string) *httptest.ResponseRecorder {
	req := runsListRequest("/workflows/"+workflowID+"/runs"+query, userID)
	req = withURLParam(req, "id", workflowID)
	w := httptest.NewRecorder()
	d.ListWorkflowRuns(w, req)
	return w
}

func decodeRunPage(t *testing.T, w *httptest.ResponseRecorder) runPageBody {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var body runPageBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// Parsing happens before the store is touched, so these run without a database.
func TestListWorkflowRunsRejectsBadLimit(t *testing.T) {
	d := &handlers.Deps{}
	for _, v := range []string{"0", "-1", "abc"} {
		t.Run(v, func(t *testing.T) {
			w := serveWorkflowRuns(d, "wf_1", "user-1", "?limit="+v)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("limit=%s: want 400 got %d", v, w.Code)
			}
		})
	}
}

func TestListRunsRejectsBadCursor(t *testing.T) {
	d := &handlers.Deps{}
	w := serveWorkflowRuns(d, "wf_1", "user-1", "?cursor=not-a-cursor")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("workflow runs: want 400 got %d", w.Code)
	}
	rec := httptest.NewRecorder()
	d.ListRecentRuns(rec, runsListRequest("/runs?cursor=not-a-cursor", "user-1"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("recent runs: want 400 got %d", rec.Code)
	}
}

func TestListWorkflowRunsDefaultLimitAndNextCursor(t *testing.T) {
	d := testDeps(t)
	owner := runsListOwner()
	wf := runsListWorkflowWithRuns(t, d, owner, 21)

	first := decodeRunPage(t, serveWorkflowRuns(d, wf, owner, ""))
	if len(first.Runs) != 20 || first.NextCursor == nil {
		t.Fatalf("first page: %d runs, nextCursor=%v; want 20 and a cursor", len(first.Runs), first.NextCursor)
	}

	second := decodeRunPage(t, serveWorkflowRuns(d, wf, owner, "?cursor="+*first.NextCursor))
	if len(second.Runs) != 1 || second.NextCursor != nil {
		t.Fatalf("second page: %d runs, nextCursor=%v; want 1 and null", len(second.Runs), second.NextCursor)
	}
	for _, r := range first.Runs {
		if r.ID == second.Runs[0].ID {
			t.Fatal("the second page repeated a row from the first")
		}
	}
}

func TestListWorkflowRunsLimitClampedTo50(t *testing.T) {
	d := testDeps(t)
	owner := runsListOwner()
	wf := runsListWorkflowWithRuns(t, d, owner, 51)

	body := decodeRunPage(t, serveWorkflowRuns(d, wf, owner, "?limit=500"))
	if len(body.Runs) != 50 || body.NextCursor == nil {
		t.Fatalf("limit=500: %d runs, nextCursor=%v; want 50 and a cursor", len(body.Runs), body.NextCursor)
	}
}

func TestListWorkflowRunsNonOwnerGets404(t *testing.T) {
	d := testDeps(t)
	wf := runsListWorkflowWithRuns(t, d, runsListOwner(), 1)

	w := serveWorkflowRuns(d, wf, "someone-else", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-owner: want 404 got %d body=%s", w.Code, w.Body.String())
	}
}

func TestListWorkflowRunsOmitsInputContext(t *testing.T) {
	d := testDeps(t)
	owner := runsListOwner()
	wf := runsListWorkflowWithRuns(t, d, owner, 1)

	w := serveWorkflowRuns(d, wf, owner, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if b := w.Body.String(); strings.Contains(b, "inputContext") || strings.Contains(b, "do-not-list") {
		t.Fatalf("run history leaked the input context: %s", b)
	}
}

func TestListRecentRunsNextCursorNullOnLastPage(t *testing.T) {
	d := testDeps(t)
	owner := runsListOwner()
	runsListWorkflowWithRuns(t, d, owner, 2)

	w := httptest.NewRecorder()
	d.ListRecentRuns(w, runsListRequest("/runs?limit=5", owner))
	if !strings.Contains(w.Body.String(), `"nextCursor":null`) {
		t.Fatalf("want an explicit null nextCursor, got %s", w.Body.String())
	}
	body := decodeRunPage(t, w)
	if len(body.Runs) != 2 {
		t.Fatalf("want 2 runs, got %d", len(body.Runs))
	}
}
