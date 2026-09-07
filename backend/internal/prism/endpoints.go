// Package prism holds the specification for PRISM's x402 endpoints: their
// URLs, their live-probed payment quotes, and the shape of the request each
// one takes.
//
// It exists as its own leaf package because two unrelated callers need the
// same facts and must never disagree about them:
//
//   - internal/bazaar builds PRISM's curated catalog entries from Catalog(),
//     so the price on a Bazaar card is literally the same value as
//   - internal/api/handlers, which builds and pays the real request from
//     Endpoints().
//
// Duplicating the table in both would let the quoted price drift from the
// charged price. A leaf package with no AgentMesh imports keeps that
// impossible without creating an import cycle in either direction.
//
// # On the quotes in this file
//
// Every PayTo and AmountMicros here was transcribed from a live 402 challenge
// probed on 2026-09-05, not from documentation and never from a guess:
//
//	curl -D - https://prism-99h2.onrender.com/<path>
//	# -> 402, payment-required: <base64 of the challenge JSON>
//
// All four agreed with PRISM's written spec exactly. Re-probe before editing
// any amount or address in this file. A wrong PayTo does not fail loudly — it
// pays a stranger, which is why bazaar's curated registry refused to carry a
// PRISM entry at all until these values were verified.
package prism

import "net/http"

// Host is PRISM's origin. Every endpoint below must live on it; a spec
// pointing anywhere else is a bug, and TestEndpointsAreWellFormed enforces it.
const Host = "prism-99h2.onrender.com"

// Provider is the display name, spelled the way PRISM spells it.
const Provider = "Prism"

// ConsoleKey marks these endpoints as backed by the PRISM console page rather
// than by a canvas node. It is a provider key, never a URL: the frontend maps
// it to a route it already owns, so no catalog data can ever redirect a user.
const ConsoleKey = "prism"

// Network is the CAIP-2 id every PRISM challenge declares (Algorand mainnet),
// and AssetID is the USDC ASA every one of them prices in.
const (
	Network = "algorand:wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8="
	AssetID = "31566704"
)

// PayTo is the single settlement address all four endpoints share (verified
// identical across all four probes on 2026-09-05).
const PayTo = "FL7U7GHUZB2R6RACPGY5UFD2K47CP2IL4RQWX7LKYE5QSFGXVJCDGPRLBE"

// Field kinds. A FieldFile carries its bytes base64-encoded and is referenced
// from a body template by two tokens, {{file:name}} and {{fileName:name}} —
// see engine/nodes.expandBodyTemplate.
const (
	FieldText     = "text"
	FieldTextarea = "textarea"
	FieldFile     = "file"
)

// Field is one input on an endpoint's console form.
type Field struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
	Description string `json:"description,omitempty"`
	// Accept is the file picker's accept attribute, for FieldFile only.
	Accept string `json:"accept,omitempty"`
}

// Task groups the two tiers of one capability. PRISM is a "multi-tier AI
// routing" platform: fast and accurate are the same task at different model
// quality, which is what lets the console show one task picker and one tier
// toggle instead of four unrelated forms.
type Task struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Endpoint is one payable PRISM endpoint.
type Endpoint struct {
	ID    string `json:"id"`
	Task  string `json:"task"`
	Tier  string `json:"tier"`
	Title string `json:"title"`
	// Description is PRISM's own, taken verbatim from the probed challenge's
	// resource.description.
	Description string `json:"description"`
	Path        string `json:"path"`
	Method      string `json:"method"`
	// AmountMicros is the vendor's price in atomic USDC (6 decimals). It is
	// NOT what the caller pays: AgentMesh's flat platform markup
	// (models.X402PlatformFeeUSDMicros) is added on top by the relay, and the
	// console surfaces the sum. See TotalCostUSDMicros.
	AmountMicros int64 `json:"amountMicros"`
	// BodyTemplate, when non-empty, is sent verbatim as the JSON body with
	// {{param:x}} / {{file:x}} / {{fileName:x}} placeholders expanded from the
	// caller's fields (models.BodyModeJSON). When empty the fields are
	// appended to the URL as query parameters instead.
	BodyTemplate string  `json:"-"`
	Fields       []Field `json:"fields"`
	// Verified records how this endpoint's request shape is known. Only
	// VerifiedLive and VerifiedDocumented shapes are known good;
	// VerifiedSibling shares its template with one of those but has not itself
	// been called. The console notes the difference, quietly -- getting a shape
	// wrong means being charged for a request the endpoint then rejects.
	Verified string `json:"verified"`
}

// URL is the endpoint's full https URL.
func (e Endpoint) URL() string { return "https://" + Host + e.Path }

// Verification states for Endpoint.Verified, strongest first.
//
// These describe how much is known about an endpoint's REQUEST shape, which is
// the only thing that can cause a paid-then-rejected call. They are not a
// quality rating.
const (
	// VerifiedLive: a real paid call to this exact endpoint succeeded and
	// returned usable data. The strongest evidence there is.
	VerifiedLive = "live"
	// VerifiedDocumented: the shape is given in PRISM's own written spec.
	VerifiedDocumented = "documented"
	// VerifiedSibling: this endpoint sends the IDENTICAL request template as a
	// sibling that is live or documented, differing only in path and model
	// tier. Strong evidence, short of a call to this path itself.
	//
	// Replaces the old "assumed" state, which lumped this together with a pure
	// guess and drove a warning far heavier than the actual risk warranted --
	// a shape shared byte-for-byte with a call that has demonstrably settled is
	// not the same as an unknown.
	VerifiedSibling = "sibling"
)

// Task keys.
const (
	TaskCodeReview   = "code-review"
	TaskResumeScreen = "resume-screen"
)

// Tier keys.
const (
	TierFast     = "fast"
	TierAccurate = "accurate"
)

// resumeScreenBody is the body PRISM's resume-screen endpoints document: a
// nested files array with the bytes base64 inside a JSON string, alongside a
// text field. No arrangement of flat key/value params produces that shape,
// which is exactly why PRISM cannot be a plain Bazaar node and has a console
// instead. Asserted end-to-end by
// engine/nodes.TestBuildTargetRequestJSONBodyModeProducesPrismShape.
const resumeScreenBody = `{"task_description":"{{param:task_description}}","files":[{"filename":"{{fileName:resume}}","content_base64":"{{file:resume}}"}]}`

// codeReviewFields is the query-parameter shape documented for
// /code-review-accurate:
//
//	GET /code-review-accurate?file_path=src/index.ts&raw_url=https://raw.githubusercontent.com/...
func codeReviewFields() []Field {
	return []Field{
		{
			Name:        "raw_url",
			Label:       "Link to the file",
			Kind:        FieldText,
			Required:    true,
			Placeholder: "https://raw.githubusercontent.com/octocat/Hello-World/master/README",
			Description: "Prism opens the file itself, so the link has to be public and point at the raw text. On GitHub, open the file and click Raw, then copy the address.",
		},
		{
			Name:        "file_path",
			Label:       "File name",
			Kind:        FieldText,
			Required:    true,
			Placeholder: "src/index.ts",
			Description: "Labels the review, and tells Prism which language to expect. Copy the end of the link above, extension included.",
		},
	}
}

func resumeScreenFields() []Field {
	return []Field{
		{
			Name:        "task_description",
			Label:       "Job description",
			Kind:        FieldTextarea,
			Required:    true,
			Placeholder: "Senior React developer, 5+ years, TypeScript, design-system experience.",
			Description: "The role you are hiring for. The more specific the must-haves, the sharper the scoring.",
		},
		{
			Name:        "resume",
			Label:       "Resume",
			Kind:        FieldFile,
			Required:    true,
			Accept:      ".pdf,.doc,.docx,.txt,.md",
			Description: "PDF, Word or plain text, up to 2 MB.",
		},
	}
}

// Tasks returns the two capabilities, in the order the console shows them.
func Tasks() []Task {
	return []Task{
		{
			Key:         TaskCodeReview,
			Title:       "Code review",
			Description: "Check one file for bugs, security holes and rough edges.",
		},
		{
			Key:         TaskResumeScreen,
			Title:       "Resume screen",
			Description: "Score a resume against the role you are hiring for.",
		},
	}
}

// Endpoints returns all four PRISM endpoints. The slice is rebuilt per call so
// no caller can mutate a shared table — Fields in particular is handed
// straight to a JSON encoder on a request path.
func Endpoints() []Endpoint {
	return []Endpoint{
		{
			ID:           "code-review-fast",
			Task:         TaskCodeReview,
			Tier:         TierFast,
			Title:        "Quick code review",
			Description:  "A quick pass over one file for bugs, security issues and obvious mistakes. Answers in seconds.",
			Path:         "/code-review-fast",
			Method:       http.MethodGet,
			AmountMicros: 100_000,
			Fields:       codeReviewFields(),
			// Same query-parameter shape as code-review-accurate, whose shape
			// PRISM documents. Only the model tier differs.
			Verified: VerifiedSibling,
		},
		{
			ID:           "code-review-accurate",
			Task:         TaskCodeReview,
			Tier:         TierAccurate,
			Title:        "Thorough code review",
			Description:  "A careful review of one file, the kind a senior engineer would give it, with a proper security pass. Slower, and catches more.",
			Path:         "/code-review-accurate",
			Method:       http.MethodGet,
			AmountMicros: 200_000,
			Fields:       codeReviewFields(),
			Verified:     VerifiedDocumented,
		},
		{
			ID:           "resume-screen-fast",
			Task:         TaskResumeScreen,
			Tier:         TierFast,
			Title:        "Quick resume screen",
			Description:  "A quick read of the resume against your role, with a match score. Good for a first sift.",
			Path:         "/resume-screen-fast",
			Method:       http.MethodPost,
			AmountMicros: 100_000,
			BodyTemplate: resumeScreenBody,
			Fields:       resumeScreenFields(),
			// Sends the identical body template as resume-screen-accurate,
			// which settled a real call on 2026-09-05.
			Verified: VerifiedSibling,
		},
		{
			ID:           "resume-screen-accurate",
			Task:         TaskResumeScreen,
			Tier:         TierAccurate,
			Title:        "Thorough resume screen",
			Description:  "A close read of the resume against your role, with a detailed match score and reasoning. Slower, and more reliable.",
			Path:         "/resume-screen-accurate",
			Method:       http.MethodPost,
			AmountMicros: 250_000,
			BodyTemplate: resumeScreenBody,
			Fields:       resumeScreenFields(),
			// Confirmed 2026-09-05 by a real paid call ($0.25 + $1.50 fee):
			// returned a ranked `candidates` array with match scores. Both
			// settlement legs and the platform fee settled on-chain.
			Verified: VerifiedLive,
		},
	}
}

// Lookup finds an endpoint by id. The second return is false for an unknown
// id, which callers must treat as a client error and never as a passthrough:
// the URL a call is made against comes from this table, never from a request
// body, so /prism/run cannot be turned into an open x402 proxy that spends a
// user's credit against an arbitrary host.
func Lookup(id string) (Endpoint, bool) {
	for _, e := range Endpoints() {
		if e.ID == id {
			return e, true
		}
	}
	return Endpoint{}, false
}
