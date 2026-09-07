package nodes

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/prism"
)

func TestBuildTargetRequestGETUsesQueryString(t *testing.T) {
	node := models.WorkflowNode{
		Endpoint:      "https://api.example/opportunities",
		ParamDefaults: map[string]string{"address": "ABC123", "limit": ""},
	}
	got, body, ct, _ := buildTargetRequest(node, http.MethodGet)
	if body != nil || ct != "" {
		t.Fatalf("GET must not carry a body, got body=%q ct=%q", body, ct)
	}
	u, err := url.Parse(got.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("address") != "ABC123" {
		t.Errorf("want address in query, got %q", got.Endpoint)
	}
	// An empty value is "not configured", not "send an empty string" — a
	// target distinguishing absent from blank would otherwise get the wrong one.
	if _, present := u.Query()["limit"]; present {
		t.Errorf("empty param must be omitted, got %q", got.Endpoint)
	}
}

func TestBuildTargetRequestNeverOverwritesExistingQueryValue(t *testing.T) {
	// executeFunctionCall appends an agent's LLM-chosen args to the URL before
	// this runs. Those are a per-call decision and must beat static config.
	node := models.WorkflowNode{
		Endpoint:      "https://api.example/x?address=FROM_AGENT",
		ParamDefaults: map[string]string{"address": "FROM_CONFIG"},
	}
	got, _, _, _ := buildTargetRequest(node, http.MethodGet)
	u, _ := url.Parse(got.Endpoint)
	if v := u.Query().Get("address"); v != "FROM_AGENT" {
		t.Errorf("agent-supplied value must win, got %q", v)
	}
}

func TestBuildTargetRequestPOSTUsesJSONBody(t *testing.T) {
	node := models.WorkflowNode{
		Endpoint:     "https://api.example/resume",
		CustomParams: []models.CustomParam{{Name: "resume", Kind: "text", Value: "8 yrs backend"}},
	}
	got, body, ct, _ := buildTargetRequest(node, http.MethodPost)
	if got.Endpoint != node.Endpoint {
		t.Errorf("POST must not rewrite the URL, got %q", got.Endpoint)
	}
	if ct != "application/json" {
		t.Errorf("want application/json, got %q", ct)
	}
	if !strings.Contains(string(body), `"resume":"8 yrs backend"`) {
		t.Errorf("want resume in JSON body, got %s", body)
	}
}

func TestBuildTargetRequestCustomParamBeatsDiscoveredDefault(t *testing.T) {
	node := models.WorkflowNode{
		Endpoint:      "https://api.example/x",
		ParamDefaults: map[string]string{"address": "DISCOVERED"},
		CustomParams:  []models.CustomParam{{Name: "address", Kind: "text", Value: "TYPED_BY_USER"}},
	}
	got, _, _, _ := buildTargetRequest(node, http.MethodGet)
	u, _ := url.Parse(got.Endpoint)
	if v := u.Query().Get("address"); v != "TYPED_BY_USER" {
		t.Errorf("hand-typed value must win over a discovered default, got %q", v)
	}
}

func TestBuildTargetRequestFileProducesParsableMultipart(t *testing.T) {
	const fileContent = "%PDF-1.4 fake resume bytes"
	node := models.WorkflowNode{
		Endpoint: "https://api.example/resume-screen",
		CustomParams: []models.CustomParam{
			{Name: "role", Kind: "text", Value: "backend"},
			{
				Name:     "resume",
				Kind:     "file",
				Value:    base64.StdEncoding.EncodeToString([]byte(fileContent)),
				FileName: "cv.pdf",
				MIMEType: "application/pdf",
			},
		},
	}
	_, body, ct, _ := buildTargetRequest(node, http.MethodPost)
	if !strings.HasPrefix(ct, "multipart/form-data") {
		t.Fatalf("want multipart content type, got %q", ct)
	}

	// Parse it back exactly as a receiving server would: a body the target
	// can't parse is the whole failure mode this encoding exists to avoid.
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("content type not parseable: %v", err)
	}
	boundary, ok := params["boundary"]
	if !ok {
		t.Fatal("content type carries no boundary — body would be unparseable")
	}
	mr := multipart.NewReader(strings.NewReader(string(body)), boundary)
	form, err := mr.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("multipart body did not parse: %v", err)
	}
	if got := form.Value["role"]; len(got) != 1 || got[0] != "backend" {
		t.Errorf("text field lost in multipart encoding: %v", got)
	}
	files := form.File["resume"]
	if len(files) != 1 {
		t.Fatalf("want 1 file part, got %d", len(files))
	}
	if files[0].Filename != "cv.pdf" {
		t.Errorf("want filename cv.pdf, got %q", files[0].Filename)
	}
	f, err := files[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, len(fileContent))
	if _, err := f.Read(buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != fileContent {
		t.Errorf("file bytes corrupted: got %q", buf)
	}
}

func TestBuildTargetRequestRejectsOversizeFile(t *testing.T) {
	// The frontend caps this too, but that check is bypassable — a workflow
	// can be PUT straight at the API.
	huge := base64.StdEncoding.EncodeToString(make([]byte, maxParamFileBytes+1))
	node := models.WorkflowNode{
		Endpoint:     "https://api.example/x",
		CustomParams: []models.CustomParam{{Name: "f", Kind: "file", Value: huge}},
	}
	_, body, ct, _ := buildTargetRequest(node, http.MethodPost)
	if body != nil || ct != "" {
		t.Errorf("oversize file must yield no body rather than a partial one, got ct=%q len=%d", ct, len(body))
	}
}

func TestBuildTargetRequestFileValueNeverLeaksIntoQueryString(t *testing.T) {
	// A file's Value is base64 bytes. Treating it as a text param would paste
	// the whole encoded blob into the URL.
	node := models.WorkflowNode{
		Endpoint:      "https://api.example/x",
		ParamDefaults: map[string]string{"doc": "should-be-dropped"},
		CustomParams: []models.CustomParam{
			{Name: "doc", Kind: "file", Value: base64.StdEncoding.EncodeToString([]byte("bytes"))},
		},
	}
	got, body, ct, _ := buildTargetRequest(node, http.MethodPost)
	if strings.Contains(got.Endpoint, "doc=") {
		t.Errorf("file param leaked into URL: %q", got.Endpoint)
	}
	if !strings.HasPrefix(ct, "multipart/") {
		t.Fatalf("want multipart, got %q", ct)
	}
	if strings.Contains(string(body), "should-be-dropped") {
		t.Error("discovered default was sent as a text field for a file-kind param")
	}
}

func TestBuildTargetRequestNoParamsIsUnchanged(t *testing.T) {
	node := models.WorkflowNode{Endpoint: "https://api.example/x"}
	got, body, ct, _ := buildTargetRequest(node, http.MethodGet)
	if got.Endpoint != node.Endpoint || body != nil || ct != "" {
		t.Errorf("unconfigured node must be untouched, got %q %q %q", got.Endpoint, body, ct)
	}
}

// TestBuildTargetRequestJSONBodyModeProducesPrismShape builds the real body
// prism-99h2.onrender.com/resume-screen-accurate documents: a nested array of
// file objects with the bytes base64 inside a JSON string, alongside a text
// field. No arrangement of flat key/value params produces that shape, which
// is why the mode exists -- the field-derived path sent multipart and the
// endpoint answered "No files provided" AFTER taking $0.25 (2026-08-03).
func TestBuildTargetRequestJSONBodyModeProducesPrismShape(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.5 fake"))
	node := models.WorkflowNode{
		BodyMode: models.BodyModeJSON,
		BodyTemplate: `{
			"task_description": "{{param:role}}",
			"files": [{"filename": "{{fileName:resume}}", "content_base64": "{{file:resume}}"}]
		}`,
		CustomParams: []models.CustomParam{
			{Kind: "text", Name: "role", Value: "Senior React Developer"},
			{Kind: "file", Name: "resume", FileName: "john_doe.pdf", MIMEType: "application/pdf", Value: pdf},
		},
	}

	_, body, contentType, err := buildTargetRequest(node, http.MethodPost)
	if err != nil {
		t.Fatalf("want the body built, got %v", err)
	}
	if contentType != "application/json" {
		t.Fatalf("want application/json, got %q", contentType)
	}
	var got struct {
		TaskDescription string `json:"task_description"`
		Files           []struct {
			Filename      string `json:"filename"`
			ContentBase64 string `json:"content_base64"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("want a valid JSON body, got error: %v (body: %s)", err, body)
	}
	if got.TaskDescription != "Senior React Developer" {
		t.Fatalf("want the text param filled in, got %q", got.TaskDescription)
	}
	if len(got.Files) != 1 {
		t.Fatalf("want one file object in the array, got %d", len(got.Files))
	}
	if got.Files[0].Filename != "john_doe.pdf" {
		t.Fatalf("want the uploaded file's own name, got %q", got.Files[0].Filename)
	}
	if got.Files[0].ContentBase64 != pdf {
		t.Fatal("want the file's base64 bytes inside the JSON string, not a separate multipart part")
	}
}

// TestBuildTargetRequestJSONBodyModeEscapesValues confirms a value cannot
// break out of the string literal it is substituted into: a filename with a
// quote in it would otherwise produce a malformed body that the endpoint
// rejects after charging for the call.
func TestBuildTargetRequestJSONBodyModeEscapesValues(t *testing.T) {
	node := models.WorkflowNode{
		BodyMode:     models.BodyModeJSON,
		BodyTemplate: `{"name": "{{fileName:doc}}"}`,
		CustomParams: []models.CustomParam{
			{Kind: "file", Name: "doc", FileName: `my "best" resume\.pdf`, Value: "AAA="},
		},
	}

	_, body, _, err := buildTargetRequest(node, http.MethodPost)
	if err != nil {
		t.Fatalf("want the body built, got %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("want valid JSON despite quotes in the value, got error: %v (body: %s)", err, body)
	}
	if got["name"] != `my "best" resume\.pdf` {
		t.Fatalf("want the filename preserved exactly, got %q", got["name"])
	}
}

// TestBuildTargetRequestJSONBodyModeRejectsUnknownField pins the loud
// failure: a template naming a field the node does not have must stop the
// call, not send null where a resume belongs. The endpoint charges either
// way, so silence here costs real money.
func TestBuildTargetRequestJSONBodyModeRejectsUnknownField(t *testing.T) {
	node := models.WorkflowNode{
		BodyMode:     models.BodyModeJSON,
		BodyTemplate: `{"files": [{"content_base64": "{{file:resume}}"}]}`,
	}

	_, _, _, err := buildTargetRequest(node, http.MethodPost)
	if err == nil {
		t.Fatal("want an error naming the missing field, got none")
	}
	if !strings.Contains(err.Error(), "{{file:resume}}") {
		t.Fatalf("want the error to name the unresolved placeholder, got %v", err)
	}
}

// TestBuildTargetRequestJSONBodyModeRejectsInvalidJSON catches a body that is
// malformed before it is sent, rather than paying to discover it.
func TestBuildTargetRequestJSONBodyModeRejectsInvalidJSON(t *testing.T) {
	node := models.WorkflowNode{
		BodyMode:     models.BodyModeJSON,
		BodyTemplate: `{"task_description": "x",}`,
	}

	if _, _, _, err := buildTargetRequest(node, http.MethodPost); err == nil {
		t.Fatal("want an error for a malformed JSON body, got none")
	}
}

// TestBuildTargetRequestParamsModeUnchanged confirms the default path is
// untouched: a node that never opts into a JSON body keeps building its
// request from fields exactly as before.
func TestBuildTargetRequestParamsModeUnchanged(t *testing.T) {
	node := models.WorkflowNode{
		Endpoint:     "https://example.test/resource",
		CustomParams: []models.CustomParam{{Kind: "text", Name: "url", Value: "https://a.test"}},
	}

	got, body, contentType, err := buildTargetRequest(node, http.MethodGet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body != nil || contentType != "" {
		t.Fatalf("want a bodyless GET, got body=%q contentType=%q", body, contentType)
	}
	if got.Endpoint != "https://example.test/resource?url=https%3A%2F%2Fa.test" {
		t.Fatalf("want the param on the query string, got %s", got.Endpoint)
	}
}

// TestPrismConsoleTemplatesExpandToTheDocumentedShape is
// TestBuildTargetRequestJSONBodyModeProducesPrismShape's counterpart, run
// against the REAL template the Prism console ships (internal/prism) rather
// than one written by hand in this file. The hand-written one proves the
// mechanism works; this one proves the shipped configuration uses it
// correctly. A template edit that breaks Prism's body now fails here instead
// of being discovered by a user who paid $1.75 for a rejected request.
func TestPrismConsoleTemplatesExpandToTheDocumentedShape(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.5 fake"))

	for _, e := range prism.Endpoints() {
		if e.BodyTemplate == "" {
			continue
		}
		node := models.WorkflowNode{
			BodyMode:     models.BodyModeJSON,
			BodyTemplate: e.BodyTemplate,
			CustomParams: []models.CustomParam{
				{Kind: "text", Name: "task_description", Value: "Senior React Developer"},
				{Kind: "file", Name: "resume", FileName: "john_doe.pdf", MIMEType: "application/pdf", Value: pdf},
			},
		}
		_, body, contentType, err := buildTargetRequest(node, http.MethodPost)
		if err != nil {
			t.Errorf("%s: %v", e.ID, err)
			continue
		}
		if contentType != "application/json" {
			t.Errorf("%s: content type %q", e.ID, contentType)
		}
		var got struct {
			TaskDescription string `json:"task_description"`
			Files           []struct {
				Filename      string `json:"filename"`
				ContentBase64 string `json:"content_base64"`
			} `json:"files"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("%s: body is not valid JSON: %v (%s)", e.ID, err, body)
			continue
		}
		if got.TaskDescription != "Senior React Developer" {
			t.Errorf("%s: task_description = %q", e.ID, got.TaskDescription)
		}
		if len(got.Files) != 1 {
			t.Errorf("%s: want one file object, got %d", e.ID, len(got.Files))
			continue
		}
		if got.Files[0].Filename != "john_doe.pdf" || got.Files[0].ContentBase64 != pdf {
			t.Errorf("%s: file object = %+v", e.ID, got.Files[0])
		}
	}
}
