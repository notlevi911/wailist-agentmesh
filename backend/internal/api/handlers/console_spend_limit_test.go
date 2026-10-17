package handlers_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agentmesh/backend/internal/api"
	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/helixbox"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/prism"
	"github.com/agentmesh/backend/internal/x402"
)

type consoleRoundTrip func(*http.Request) (*http.Response, error)

func (f consoleRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type consolePaymentStub struct {
	signer      fakeUSDCSigner
	quotes      atomic.Int64
	payments    atomic.Int64
	settlements atomic.Int64
	listings    atomic.Int64
	rejectFirst bool
}

func stubConsolePayments(t *testing.T, d *handlers.Deps, vendor int64) *consolePaymentStub {
	t.Helper()
	s := &consolePaymentStub{signer: fakeUSDCSigner{group: []string{"inert-test-payment"}}}
	challenge := fmt.Sprintf(`{"x402Version":2,"accepts":[{"scheme":"exact","network":"algorand:testnet","payTo":"TESTPAYEE","asset":"10458941","amount":"%d","extra":{"feePayer":"TESTFEEPAYER"}}]}`, vendor)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x402/relay" {
			t.Errorf("unexpected relay path: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Payment") == "" {
			s.quotes.Add(1)
			w.WriteHeader(http.StatusPaymentRequired)
			io.WriteString(w, challenge)
			return
		}
		attempt := s.payments.Add(1)
		if s.rejectFirst && attempt == 1 {
			w.WriteHeader(http.StatusPaymentRequired)
			io.WriteString(w, `{"error":"inert payment rejected"}`)
			return
		}
		w.Header().Set("X-Inbound-Settled", "true")
		w.Header().Set("X-Settlement-TxId", "TESTTX")
		io.WriteString(w, `{"result":"test review"}`)
	}))
	t.Cleanup(relay.Close)
	facilitator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			io.WriteString(w, `{"isValid":true}`)
			return
		}
		if r.URL.Path != "/settle" {
			t.Errorf("unexpected facilitator path: %s", r.URL.Path)
		}
		s.settlements.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"transaction":"TESTFEETX"}`)
	}))
	t.Cleanup(facilitator.Close)
	oldDefault, oldSafe := http.DefaultTransport, nodes.SafeHTTPClient().Transport
	transport := consoleRoundTrip(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, ""
		switch r.URL.Hostname() {
		case "127.0.0.1":
			return oldDefault.RoundTrip(r)
		case "api.github.com":
			if r.URL.Path != "/repos/example/project/git/trees/main" {
				return nil, fmt.Errorf("unexpected tree path: %s", r.URL.Path)
			}
			s.listings.Add(1)
			body = `{"tree":[{"path":"one.go","type":"blob","size":100},{"path":"two.go","type":"blob","size":100}]}`
		case prism.Host, helixbox.Host:
			if r.Header.Get("X-Payment") != "" || r.Header.Get("Payment-Signature") != "" {
				t.Error("unexpected direct payment to target")
			}
			if r.URL.Path == "/v2/session-status" {
				body = `{"exists":true,"paid":false}`
			} else {
				status, body = http.StatusPaymentRequired, challenge
			}
		default:
			return nil, fmt.Errorf("outbound request blocked by test: %s", r.URL.Hostname())
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	http.DefaultTransport, nodes.SafeHTTPClient().Transport = transport, transport
	t.Cleanup(func() { http.DefaultTransport, nodes.SafeHTTPClient().Transport = oldDefault, oldSafe })
	d.USDCSigner = &s.signer
	d.USDCAssetID = 10458941
	d.PlatformSpendWalletEncMnemonic = "inert-test-wallet"
	d.PlatformWalletAddress = "TESTPLATFORM"
	d.RelayFeePayer = "TESTFEEPAYER"
	d.RelayNetwork = "algorand:testnet"
	d.RelayBaseURL = relay.URL
	d.FrontendURL = "https://example.test"
	d.FacilitatorClient = x402.NewFacilitatorClient(facilitator.URL)
	return s
}

func consoleTestAccount(t *testing.T, d *handlers.Deps, limit int64, save bool) models.User {
	t.Helper()
	owner := deletionUser(t, d, "")
	if err := d.Store.ReleaseReservedCredits(t.Context(), owner.ID, 20_000_000); err != nil {
		t.Fatal(err)
	}
	if save {
		patch := models.UserSettingsPatch{SetMaxCallSpend: true}
		if limit > 0 {
			patch.MaxCallSpendUSDMicros = &limit
		}
		if _, err := d.Store.UpsertUserSettings(t.Context(), owner.ID, patch); err != nil {
			t.Fatal(err)
		}
	}
	return owner
}

func assertConsoleCharge(t *testing.T, d *handlers.Deps, owner models.User, workflowName string, wantCharge int64, wantRows int, wantStatus string) {
	t.Helper()
	balance, err := d.Store.GetCreditBalance(t.Context(), owner.ID)
	if err != nil || balance != 20_000_000-wantCharge {
		t.Errorf("balance=%d, want=%d, err=%v", balance, 20_000_000-wantCharge, err)
	}
	wf, found, err := d.Store.FindSystemWorkflow(t.Context(), owner.ID, workflowName)
	if err != nil || !found {
		t.Fatalf("workflow found=%v err=%v", found, err)
	}
	runs, err := d.Store.ListWorkflowRuns(t.Context(), owner.ID, wf.ID, nil, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	rows, err := d.Store.ListDebitLedger(t.Context(), runs[0].ID)
	var total int64
	for _, row := range rows {
		total += row.AmountUSDMicros
	}
	if err != nil || len(rows) != wantRows || total != wantCharge || string(runs[0].Status) != wantStatus {
		t.Errorf("ledger rows=%d total=%d run=%s, want rows=%d total=%d run=%s, err=%v", len(rows), total, runs[0].Status, wantRows, wantCharge, wantStatus, err)
	}
}

func TestConsoleSpendLimitRoutes(t *testing.T) {
	const vendor = int64(100_000)
	total := vendor + models.X402PlatformFeeUSDMicros
	for _, tc := range []struct {
		name, path, body, workflow string
		limit                      int64
		save, allowed              bool
	}{
		{"prism review example", "/prism/run", "", "", 50_000, true, false},
		{"prism includes fee", "/prism/run", "", "", total - 1, true, false},
		{"prism exact boundary", "/prism/run", "", "", total, true, true},
		{"prism missing settings", "/prism/run", "", "", 0, false, true},
		{"prism disabled ceiling", "/prism/run", "", "", 0, true, true},
		{"helix includes fee", "/helixbox/run", `{"endpoint":"cli-hour","fields":{"code":"test-session"}}`, "HelixBox Console (managed, do not edit)", total - 1, true, false},
		{"helix exact boundary", "/helixbox/run", `{"endpoint":"cli-hour","fields":{"code":"test-session"}}`, "HelixBox Console (managed, do not edit)", total, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEB_READONLY_MODE", "false")
			d := testDeps(t)
			owner := consoleTestAccount(t, d, tc.limit, tc.save)
			other := consoleTestAccount(t, d, 50_000, true)
			s := stubConsolePayments(t, d, vendor)
			if tc.body == "" {
				tc.body = `{"endpoint":"code-review-fast","fields":{"raw_url":{"kind":"text","value":"https://example.test/one.go"},"file_path":{"kind":"text","value":"one.go"}}}`
				tc.workflow = "Prism Console (managed, do not edit)"
			}
			rec := settingsRequest(api.NewRouter(d), "POST", tc.path, api.TestMakeToken(deletionSecret, owner.ID), tc.body)
			wantHTTP, wantCharge, wantRows, wantSigns, wantStatus := 402, int64(0), 0, 0, "failed"
			if tc.allowed {
				wantHTTP, wantCharge, wantRows, wantSigns, wantStatus = 200, total, 2, 2, "success"
			}
			t.Logf("status=%d signs=%d payments=%d feeSettlements=%d", rec.Code, len(s.signer.calls), s.payments.Load(), s.settlements.Load())
			if rec.Code != wantHTTP || len(s.signer.calls) != wantSigns || s.payments.Load() != int64(wantSigns/2) || s.settlements.Load() != int64(wantSigns/2) {
				t.Errorf("status=%d body=%s signs=%d payments=%d feeSettlements=%d", rec.Code, rec.Body, len(s.signer.calls), s.payments.Load(), s.settlements.Load())
			}
			if !tc.allowed && !strings.Contains(rec.Body.String(), "spend limit") {
				t.Errorf("missing limit explanation: %s", rec.Body)
			}
			assertConsoleCharge(t, d, owner, tc.workflow, wantCharge, wantRows, wantStatus)
			if balance, err := d.Store.GetCreditBalance(t.Context(), other.ID); err != nil || balance != 20_000_000 {
				t.Errorf("other owner's balance=%d err=%v", balance, err)
			}
		})
	}
}

func TestRepoReviewSpendLimitRoute(t *testing.T) {
	const vendor = int64(100_000)
	fee := models.X402PlatformFeeUSDMicros
	for _, tc := range []struct {
		name                 string
		limit                int64
		paths                []string
		wantCalls            int
		wantCharge           int64
		wantHTTP, wantFiles  int
		rejectFirst, unsaved bool
	}{
		{"fee blocked", 50_000, []string{"one.go"}, 0, 0, 402, 0, false, false},
		{"fee and vendor blocked", fee + vendor - 1, []string{"one.go"}, 0, 0, 200, 1, false, false},
		{"cumulative second file blocked", fee + vendor, []string{"one.go", "two.go"}, 1, fee + vendor, 200, 2, false, false},
		{"batch exact boundary", fee + 2*vendor, []string{"one.go", "two.go"}, 2, fee + 2*vendor, 200, 2, false, false},
		{"released payment restores headroom", fee + vendor, []string{"one.go", "two.go"}, 2, fee + vendor, 200, 2, true, false},
		{"missing settings", 0, []string{"one.go", "two.go"}, 2, fee + 2*vendor, 200, 2, false, true},
		{"disabled ceiling", 0, []string{"one.go", "two.go"}, 2, fee + 2*vendor, 200, 2, false, false},
		{"no settled files release fee", fee + vendor, []string{"one.go"}, 1, 0, 200, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEB_READONLY_MODE", "false")
			d := testDeps(t)
			owner := consoleTestAccount(t, d, tc.limit, !tc.unsaved)
			s := stubConsolePayments(t, d, vendor)
			s.rejectFirst = tc.rejectFirst
			body, _ := json.Marshal(map[string]any{"repo": "example/project", "ref": "main", "paths": tc.paths})
			rec := settingsRequest(api.NewRouter(d), "POST", "/prism/repo/review", api.TestMakeToken(deletionSecret, owner.ID), string(body))
			feeCalls, wantRows, status := 0, 0, "failed"
			if tc.wantCharge > 0 {
				feeCalls, wantRows, status = 1, int((tc.wantCharge-fee)/vendor)+1, "success"
			}
			t.Logf("status=%d signs=%d payments=%d feeSettlements=%d", rec.Code, len(s.signer.calls), s.payments.Load(), s.settlements.Load())
			if rec.Code != tc.wantHTTP || s.payments.Load() != int64(tc.wantCalls) || len(s.signer.calls) != tc.wantCalls+feeCalls || s.settlements.Load() != int64(feeCalls) {
				t.Errorf("status=%d body=%s signs=%d payments=%d feeSettlements=%d", rec.Code, rec.Body, len(s.signer.calls), s.payments.Load(), s.settlements.Load())
			}
			if tc.wantHTTP == 402 && s.listings.Load() != 0 {
				t.Errorf("fee rejection still listed repository: %d", s.listings.Load())
			}
			if tc.wantHTTP == 200 {
				var result struct {
					Total   int64 `json:"totalUsdMicros"`
					Results []struct {
						Error string `json:"error"`
					} `json:"results"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Total != tc.wantCharge || len(result.Results) != tc.wantFiles {
					t.Errorf("result=%s err=%v", rec.Body, err)
				} else if !tc.rejectFirst && tc.wantCalls < tc.wantFiles && !strings.Contains(result.Results[len(result.Results)-1].Error, "spend limit") {
					t.Errorf("missing limit explanation: %s", rec.Body)
				}
			}
			assertConsoleCharge(t, d, owner, "Prism Console (managed, do not edit)", tc.wantCharge, wantRows, status)
		})
	}
}

func TestConsolePaymentRefusalRecoveryRoutes(t *testing.T) {
	for _, route := range []struct{ name, path, body, workflow string }{
		{"prism", "/prism/run", `{"endpoint":"code-review-fast","fields":{"raw_url":{"kind":"text","value":"https://example.test/one.go"},"file_path":{"kind":"text","value":"one.go"}}}`, "Prism Console (managed, do not edit)"},
		{"helixbox", "/helixbox/run", `{"endpoint":"cli-hour","fields":{"code":"test-session"}}`, "HelixBox Console (managed, do not edit)"},
		{"repository", "/prism/repo/review", `{"repo":"example/project","ref":"main","paths":["one.go"]}`, "Prism Console (managed, do not edit)"},
	} {
		for _, code := range []string{"spending_limit", "insufficient_credits"} {
			t.Run(route.name+"/"+code, func(t *testing.T) {
				t.Setenv("WEB_READONLY_MODE", "false")
				d := testDeps(t)
				limit, balance := int64(50_000), int64(20_000_000)
				if code == "insufficient_credits" {
					limit, balance = 0, 0
				}
				owner := consoleTestAccount(t, d, limit, true)
				if balance == 0 {
					if err := d.Store.ReserveCredits(t.Context(), owner.ID, 20_000_000); err != nil {
						t.Fatal(err)
					}
				}
				s := stubConsolePayments(t, d, 100_000)
				rec := settingsRequest(api.NewRouter(d), "POST", route.path, api.TestMakeToken(deletionSecret, owner.ID), route.body)
				var response struct {
					Code  string `json:"code"`
					Error string `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != 402 || response.Code != code || response.Error == "" {
					t.Errorf("refusal status=%d body=%s err=%v, want code=%s", rec.Code, rec.Body, err, code)
				}
				if len(s.signer.calls) != 0 || s.payments.Load() != 0 || s.settlements.Load() != 0 {
					t.Errorf("refusal signed or paid: signs=%d payments=%d settlements=%d", len(s.signer.calls), s.payments.Load(), s.settlements.Load())
				}
				if got, err := d.Store.GetCreditBalance(t.Context(), owner.ID); err != nil || got != balance {
					t.Errorf("balance=%d want=%d err=%v", got, balance, err)
				}
				wf, found, err := d.Store.FindSystemWorkflow(t.Context(), owner.ID, route.workflow)
				if err != nil || !found {
					t.Fatalf("workflow found=%v err=%v", found, err)
				}
				runs, err := d.Store.ListWorkflowRuns(t.Context(), owner.ID, wf.ID, nil, 10)
				if err != nil || len(runs) != 1 || runs[0].Status != models.RunStatusFailed {
					t.Fatalf("runs=%v err=%v", runs, err)
				}
				rows, err := d.Store.ListDebitLedger(t.Context(), runs[0].ID)
				if err != nil || len(rows) != 0 {
					t.Errorf("refusal wrote charges: rows=%v err=%v", rows, err)
				}
				if route.name == "repository" && s.listings.Load() != 0 {
					t.Error("rejected fee still fetched repository files")
				}
			})
		}
	}
}
