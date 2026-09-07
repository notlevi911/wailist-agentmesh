package nodes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/backend/internal/models"
)

// fakeRC is a minimal RunContexter for tests in this (internal) package that
// need one but don't care about Set/Get/ToolOutputs bookkeeping.
type fakeRC struct{ message string }

func (f *fakeRC) Message() string             { return f.message }
func (f *fakeRC) LastOutput() any             { return f.message }
func (f *fakeRC) UserInput() string           { return f.message }
func (f *fakeRC) ToolOutputs() map[string]any { return nil }
func (f *fakeRC) Set(string, any)             {}
func (f *fakeRC) Get(string) (any, bool)      { return nil, false }
func (f *fakeRC) OutputOrder() []string       { return nil }

func TestWebSearchReturnsAnswerAndSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{"parts": []map[string]any{
						{"text": "The answer is 42."},
					}},
					"groundingMetadata": map[string]any{
						"groundingChunks": []map[string]any{
							{"web": map[string]any{"uri": "https://example.com/a", "title": "Example A"}},
							{"web": map[string]any{"uri": ""}}, // no uri -- must be skipped
						},
					},
				},
			},
		})
	}))
	defer srv.Close()
	SetGeminiBaseURL(srv.URL)
	defer SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	result, err := webSearch(context.Background(), "what is the answer", "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if m["answer"] != "The answer is 42." {
		t.Fatalf("unexpected answer: %v", m["answer"])
	}
	sources, ok := m["sources"].([]map[string]string)
	if !ok || len(sources) != 1 || sources[0]["uri"] != "https://example.com/a" {
		t.Fatalf("unexpected sources: %+v", m["sources"])
	}
}

func TestWebSearchRejectsEmptyQuery(t *testing.T) {
	if _, err := webSearch(context.Background(), "   ", "test-key"); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestWebSearchRejectsMissingKey(t *testing.T) {
	if _, err := webSearch(context.Background(), "hello", ""); err == nil {
		t.Fatal("expected error for missing platform Gemini key")
	}
}

func TestWebsearchQueryPrefersLLMArgOverMessage(t *testing.T) {
	rc := &fakeRC{message: "original run message"}
	got := websearchQuery(map[string]any{"query": "llm chosen query"}, rc)
	if got != "llm chosen query" {
		t.Fatalf("want LLM arg, got %q", got)
	}
}

func TestWebsearchQueryFallsBackToMessageWhenNoArg(t *testing.T) {
	rc := &fakeRC{message: "original run message"}
	got := websearchQuery(nil, rc)
	if got != "original run message" {
		t.Fatalf("want fallback to rc.Message(), got %q", got)
	}
}

func TestExecuteToolWebsearchUsesPlatformKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{{"text": "ok"}}}},
			},
		})
	}))
	defer srv.Close()
	SetGeminiBaseURL(srv.URL)
	defer SetGeminiBaseURL("https://generativelanguage.googleapis.com")

	SetPlatformKeys(map[string]string{"gemini": "test-key"})
	defer SetPlatformKeys(nil)

	node := models.WorkflowNode{ID: "t1", Type: models.NodeTypeTool, Template: "websearch"}
	rc := &fakeRC{message: "fallback query"}
	result, err := ExecuteToolWithArgs(context.Background(), node, rc, map[string]any{"query": "real query"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok || m["answer"] != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestExecuteToolWebsearchWithoutPlatformKeyFails(t *testing.T) {
	SetPlatformKeys(nil)
	node := models.WorkflowNode{ID: "t1", Type: models.NodeTypeTool, Template: "websearch"}
	rc := &fakeRC{message: "fallback query"}
	if _, err := ExecuteTool(context.Background(), node, rc); err == nil {
		t.Fatal("expected error when no platform Gemini key is configured")
	}
}
