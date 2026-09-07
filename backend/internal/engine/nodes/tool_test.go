package nodes_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/engine"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
)

func TestCalculator(t *testing.T) {
	node := models.WorkflowNode{ID: "t1", Type: models.NodeTypeTool, Template: "calc", URL: "2 + 2 * 3"}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteTool(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	if result != "8" {
		t.Fatalf("want 8 got %v", result)
	}
}

func TestDatetime(t *testing.T) {
	node := models.WorkflowNode{ID: "t2", Type: models.NodeTypeTool, Template: "datetime"}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteTool(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := result.(string)
	if !ok || !strings.Contains(s, "T") {
		t.Fatalf("want RFC3339 got %v", result)
	}
}

func TestHTTPTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "t3", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"}
	rc := engine.NewRunContext("r1", nil)
	result, err := nodes.ExecuteTool(context.Background(), node, rc)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok || m["status"] != "ok" {
		t.Fatalf("want {status:ok} got %v", result)
	}
}

// Every tool template offered in the palette must reach a real
// implementation. ExecuteTool's default case silently echoes the upstream
// output (`return rc.Message(), nil`) with no error — a template that falls
// through to it renders as a successful node that did nothing, the same bug
// class TestEveryNewActionTemplateIsDispatched guards for actions. Each
// template is run unconfigured against a distinctive marker set as the
// upstream output; a template wired correctly either errors on missing
// config or transforms the marker into something else, but never returns it
// unchanged.
func TestEveryNewToolTemplateIsDispatched(t *testing.T) {
	const marker = "AGENTMESH_DISPATCH_MARKER_9f3c"
	templates := []string{
		"set", "json_extract", "crypto", "datetime", "xml",
		"template", "html_extract", "markdown", "quickchart",
	}
	for _, tpl := range templates {
		t.Run(tpl, func(t *testing.T) {
			rc := engine.NewRunContext("r1", nil)
			rc.Set("n0", marker)
			node := models.WorkflowNode{ID: "n1", Type: models.NodeTypeTool, Template: tpl}
			got, _ := nodes.ExecuteTool(context.Background(), node, rc)
			if got == marker {
				t.Errorf("template %q fell through to ExecuteTool's default branch — "+
					"it silently echoes the upstream output instead of doing anything", tpl)
			}
		})
	}
}

func TestHTTPTool_PutWithTemplateSendsBody(t *testing.T) {
	// PUT (like PATCH/DELETE) only attaches a body when the node explicitly
	// opts in via httpBodyTemplate -- a node saved before this file added
	// method-aware bodies never sent one for PUT, and defaulting it now
	// would silently change behavior for every such already-saved node.
	var gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{
		ID: "t4", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "PUT",
		Config: map[string]string{"httpBodyTemplate": "{{ result }}"},
	}
	rc := engine.NewRunContext("r1", []byte(`{"name":"x"}`))
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PUT" {
		t.Errorf("want PUT, got %s", gotMethod)
	}
	if !strings.Contains(gotBody, "name") {
		t.Errorf("want body to carry the message, got %q", gotBody)
	}
}

func TestHTTPTool_PutWithoutTemplateSendsNoBody(t *testing.T) {
	var gotContentLength int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentLength = r.ContentLength
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "t4b", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "PUT"}
	rc := engine.NewRunContext("r1", []byte(`{"name":"x"}`))
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotContentLength > 0 {
		t.Errorf("want no body on a PUT without an explicit httpBodyTemplate, got Content-Length %d", gotContentLength)
	}
}

func TestHTTPTool_GetSendsNoBody(t *testing.T) {
	var gotContentLength int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentLength = r.ContentLength
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "t5", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"}
	rc := engine.NewRunContext("r1", []byte(`{"name":"x"}`))
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotContentLength > 0 {
		t.Errorf("want no body on GET, got Content-Length %d", gotContentLength)
	}
}

func TestHTTPTool_AppliesCustomHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{
		ID: "t6", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET",
		Secrets: map[string]string{"httpHeadersJSON": `{"X-Api-Key":"secret123"}`},
	}
	rc := engine.NewRunContext("r1", nil)
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotHeader != "secret123" {
		t.Errorf("want custom header applied, got %q", gotHeader)
	}
}

func TestHTTPTool_AppliesBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	var gotOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{
		ID: "t7", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET",
		Secrets: map[string]string{"httpBasicUser": "alice", "httpBasicPass": "hunter2"},
	}
	rc := engine.NewRunContext("r1", nil)
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if !gotOK || gotUser != "alice" || gotPass != "hunter2" {
		t.Errorf("want basic auth alice/hunter2, got %q/%q ok=%v", gotUser, gotPass, gotOK)
	}
}

func TestHTTPTool_InvalidHeadersJSONErrors(t *testing.T) {
	node := models.WorkflowNode{
		ID: "t8", Type: models.NodeTypeTool, Template: "http", URL: "http://example.com", Method: "GET",
		Secrets: map[string]string{"httpHeadersJSON": `not json`},
	}
	rc := engine.NewRunContext("r1", nil)
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err == nil {
		t.Fatal("want error for invalid headers JSON, got nil")
	}
}

func TestHTTPTool_AppliesBodyTemplate(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{
		ID: "t9", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "POST",
		Config: map[string]string{"httpBodyTemplate": `{"summary":"{{ result.extract }}"}`},
	}
	rc := engine.NewRunContext("r1", nil)
	rc.Set("h1", map[string]any{"extract": "Algorand is fast."})
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	want := `{"summary":"Algorand is fast."}`
	if gotBody != want {
		t.Errorf("want templated body %q, got %q", want, gotBody)
	}
}

func TestHTTPTool_DeleteWithoutTemplateSendsNoBody(t *testing.T) {
	var gotMethod string
	var gotContentLength int64
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentLength = r.ContentLength
		gotContentType = r.Header.Get("Content-Type")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "t11", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "DELETE"}
	rc := engine.NewRunContext("r1", []byte(`{"name":"x"}`))
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("want DELETE, got %s", gotMethod)
	}
	if gotContentLength > 0 {
		t.Errorf("want no body on a DELETE without an explicit httpBodyTemplate, got Content-Length %d", gotContentLength)
	}
	if gotContentType != "" {
		t.Errorf("want no Content-Type header on a bodyless DELETE, got %q", gotContentType)
	}
}

func TestHTTPTool_DeleteWithTemplateSendsBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{
		ID: "t12", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "DELETE",
		Config: map[string]string{"httpBodyTemplate": `{"reason":"cleanup"}`},
	}
	rc := engine.NewRunContext("r1", nil)
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotBody != `{"reason":"cleanup"}` {
		t.Errorf("want the explicit template body on an opted-in DELETE, got %q", gotBody)
	}
}

func TestHTTPTool_NoBodyTemplateSendsRawMessageAsBefore(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "t10", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "POST"}
	rc := engine.NewRunContext("r1", []byte(`"raw message"`))
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotBody != "raw message" {
		t.Errorf("want unchanged raw-message behavior, got %q", gotBody)
	}
}

func TestHTTPTool_ErrorStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "t13", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "GET"}
	rc := engine.NewRunContext("r1", nil)
	_, err := nodes.ExecuteTool(context.Background(), node, rc)
	if err == nil {
		t.Fatal("want error on a 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("want error to mention the status code, got %q", err.Error())
	}
}

func TestHTTPTool_LowercaseMethodPreservesLegacyNoBodyBehavior(t *testing.T) {
	// A node persisted with a non-canonical-case Method (only reachable via
	// direct API use, never via the Inspector's <select>, which always
	// emits uppercase) must keep behaving exactly as it did before method
	// values were case-normalized for dispatch -- no silent body attach on
	// its next run.
	var gotMethod string
	var gotContentLength int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentLength = r.ContentLength
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	node := models.WorkflowNode{ID: "t14", Type: models.NodeTypeTool, Template: "http", URL: srv.URL, Method: "post"}
	rc := engine.NewRunContext("r1", []byte(`{"name":"x"}`))
	if _, err := nodes.ExecuteTool(context.Background(), node, rc); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("want method normalized to POST on the wire, got %s", gotMethod)
	}
	if gotContentLength > 0 {
		t.Errorf("want no body for a legacy lowercase-\"post\" node, got Content-Length %d", gotContentLength)
	}
}
