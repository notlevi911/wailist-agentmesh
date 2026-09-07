package tendril

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type fakeSigner struct{ notes []string }

func (f *fakeSigner) SignZeroSelfPayment(_ context.Context, _, note, _, _ string) (string, string, error) {
	f.notes = append(f.notes, note)
	return "c2lnbmVk", "WALLET2ADDR", nil
}

func sessionServer(t *testing.T, logins *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/platform":
			w.Write([]byte(`{"network":"algorand:wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8=","asset":{"id":"31566704","decimals":6,"symbol":"USDC"}}`))
		case "/auth/wallet-nonce":
			if got := r.URL.Query().Get("address"); got != "WALLET2ADDR" {
				t.Errorf("nonce requested for %q, want WALLET2ADDR", got)
			}
			w.Write([]byte(`{"nonce":"NONCE-1"}`))
		case "/auth/wallet-login":
			atomic.AddInt32(logins, 1)
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["nonce"] != "NONCE-1" || body["payment"] != "c2lnbmVk" {
				t.Errorf("login body = %v", body)
			}
			w.Write([]byte(`{"token":"TOK","balanceAtomic":12500000}`))
		case "/wallet":
			if r.Header.Get("Authorization") != "Bearer TOK" {
				t.Errorf("auth = %q", r.Header.Get("Authorization"))
			}
			w.Write([]byte(`{"address":"WALLET2ADDR","balanceAtomic":12500000}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSessionSignsNonceAndReadsBalance(t *testing.T) {
	var logins int32
	srv := sessionServer(t, &logins)
	defer srv.Close()

	signer := &fakeSigner{}
	sess, err := NewClient(srv.URL).Session(context.Background(), signer, "enc-mnemonic")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	bal, err := sess.Balance(context.Background())
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if bal != 12_500_000 {
		t.Errorf("balance = %d, want 12500000", bal)
	}
	// login() signs twice: once with an empty note to derive the address
	// before it knows the nonce, once with the real nonce to authenticate.
	if len(signer.notes) != 2 || signer.notes[1] != "NONCE-1" {
		t.Errorf("signed notes = %v, want [\"\", NONCE-1]", signer.notes)
	}
}

// A session is a 7-day token; re-reading the balance must not re-login.
func TestSessionReusesToken(t *testing.T) {
	var logins int32
	srv := sessionServer(t, &logins)
	defer srv.Close()

	sess, err := NewClient(srv.URL).Session(context.Background(), &fakeSigner{}, "enc")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := sess.Balance(context.Background()); err != nil {
			t.Fatalf("Balance %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&logins); got != 1 {
		t.Errorf("logins = %d, want 1", got)
	}
}
