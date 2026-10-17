package nodes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/tendril"
	"github.com/agentmesh/backend/internal/wallet"
)

const testEncKey = "0123456789abcdef0123456789abcdef"

// RequiredCreditAtomic reserves ONLY the metered hourly time from the user's
// Tendril credit. The flat 1¢ gate fee for the rent call itself is a
// separate real payment billed directly in AgentMesh credit by the relay
// (see tendrilRentGateFeeAtomic's doc comment) -- folding it in here used to
// double-charge the user for the same gate fee in two different ledgers.
func TestRequiredCreditAtomic(t *testing.T) {
	cases := []struct {
		name  string
		rate  int64
		hours float64
		want  int64
	}{
		{"two hours at six dollars", 6_000_000, 2, 12_000_000},
		{"one hour at six dollars", 6_000_000, 1, 6_000_000},
		{"half hour at one fifty", 1_500_000, 0.5, 750_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RequiredCreditAtomic(tc.rate, tc.hours); got != tc.want {
				t.Errorf("RequiredCreditAtomic(%d, %v) = %d, want %d", tc.rate, tc.hours, got, tc.want)
			}
		})
	}
}

// Hours come off a canvas text field, so every rejection here is a rejection
// of real money being spent on a nonsense duration.
func TestParseHours(t *testing.T) {
	ok := map[string]float64{"1": 1, "2": 2, "0.5": 0.5, " 3 ": 3, "": 1}
	for in, want := range ok {
		got, err := parseHours(in)
		if err != nil {
			t.Errorf("parseHours(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseHours(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"0", "-1", "abc", "1e9", "25"} {
		if _, err := parseHours(bad); err == nil {
			t.Errorf("parseHours(%q) should have errored", bad)
		}
	}
}

// executeTendrilTopup must refuse to mint Tendril credit when the underlying
// x402 call didn't actually settle a real payment -- ExecuteTool402V2
// returns a "successful" Tool402PaymentResult with SettledUSDMicros == 0
// whenever the target answers with anything other than a 402 (an outage, a
// maintenance page, a misconfigured platform spend wallet). Before this
// guard, that shape looked identical to a real settlement and
// ConvertCreditsToTendril was called anyway, minting Tendril credit backed
// by nothing.
func TestTopupRefusesToCreditWithoutRealSettlement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/platform":
			w.Write([]byte(`{}`))
		default:
			// A 200, not a 402 -- e.g. Tendril mid-outage answering every
			// path the same way, or a misconfigured proxy. No payment
			// challenge was ever issued, so no payment could have settled.
			w.Write([]byte(`{"status":"degraded"}`))
		}
	}))
	defer srv.Close()

	store := &fakeTendrilStore{agentMeshCredit: 10_000_000}
	_, err := executeTendrilTopup(context.Background(), models.WorkflowNode{TendrilAmount: "5"}, TendrilConfig{
		Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1",
	})
	if err == nil {
		t.Fatal("want an error when the topup call never actually settled a payment")
	}
	if store.tendrilCredit != 0 {
		t.Errorf("tendril credit = %d, want 0 -- must not mint credit without a real settlement", store.tendrilCredit)
	}
}

// A topup with a minimum balance must not touch Tendril at all when the
// user already holds enough credit -- no platform lookup, no payment.
func TestTopupSkipsWhenCreditAtOrAboveMinimum(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := &fakeTendrilStore{tendrilCredit: 2_000_000, agentMeshCredit: 10_000_000}
	out, err := executeTendrilTopup(context.Background(),
		models.WorkflowNode{TendrilAmount: "5", TendrilMinBalance: "2"},
		TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1"})
	if err != nil {
		t.Fatalf("skip path errored: %v", err)
	}
	m, _ := out.(map[string]any)
	if m["skipped"] != true {
		t.Errorf("output = %v, want skipped: true", out)
	}
	if calls != 0 {
		t.Errorf("Tendril was called %d time(s), want 0 on the skip path", calls)
	}
	if store.tendrilCredit != 2_000_000 {
		t.Errorf("tendril credit = %d, want unchanged 2000000", store.tendrilCredit)
	}
}

// Below the minimum, the topup proceeds exactly as an unconditional one
// would -- here that means reaching Tendril (which then refuses to settle).
func TestTopupProceedsWhenCreditBelowMinimum(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cases := []struct {
		credit int64
		min    string
	}{
		{500_000, "2"},
		{0, "2"},
		// Rounds UP to 1 micro, so it still tops up at $0 rather than
		// rounding down to a 0 threshold that every balance satisfies.
		{0, "0.0000001"},
	}
	for _, tc := range cases {
		paths = nil
		store := &fakeTendrilStore{tendrilCredit: tc.credit, agentMeshCredit: 10_000_000}
		_, err := executeTendrilTopup(context.Background(),
			models.WorkflowNode{TendrilAmount: "5", TendrilMinBalance: tc.min},
			TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1"})
		if err == nil || !strings.Contains(err.Error(), "did not settle") {
			t.Errorf("credit %d, min %s: err = %v, want the topup path's no-settlement error", tc.credit, tc.min, err)
		}
		if !slices.Contains(paths, "/topup") {
			t.Errorf("credit %d, min %s: requests %v never reached /topup", tc.credit, tc.min, paths)
		}
	}
}

// coverMarket serves a Tendril registry whose cheapest online machine costs
// rate dollars an hour, and records the amount of any /topup attempt.
func coverMarket(t *testing.T, rate float64, minTopUp int64) (*httptest.Server, *[]string, *string) {
	t.Helper()
	var paths []string
	var amount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/explorer":
			fmt.Fprintf(w, `{"nodes":[
				{"id":"offline","status":"offline","pricePerHourUsd":0.01},
				{"id":"pricey","status":"online","pricePerHourUsd":%v},
				{"id":"cheap","status":"online","pricePerHourUsd":%v}]}`, rate*10, rate)
		case "/platform":
			fmt.Fprintf(w, `{"minTopUpAtomic":%d,"maxTopUpAtomic":1000000000}`, minTopUp)
		case "/topup":
			amount = r.URL.Query().Get("amount")
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths, &amount
}

// Covering a rent skips without paying when credit already reserves the
// cheapest machine for the requested hours.
func TestTopupCoverSkipsWhenRentIsCovered(t *testing.T) {
	srv, paths, _ := coverMarket(t, 8, 100_000)
	// $8/hr for 0.25h needs $2.00; the user holds exactly that.
	store := &fakeTendrilStore{tendrilCredit: 2_000_000, agentMeshCredit: 10_000_000}
	out, err := executeTendrilTopup(context.Background(),
		models.WorkflowNode{TendrilAmount: "2", TendrilCoverHours: "0.25", TendrilMinBalance: "50"},
		TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1"})
	if err != nil {
		t.Fatalf("skip path errored: %v", err)
	}
	m, _ := out.(map[string]any)
	if m["skipped"] != true || m["machine"] != "cheap" {
		t.Errorf("output = %v, want skipped on machine cheap", out)
	}
	if slices.Contains(*paths, "/topup") || slices.Contains(*paths, "/platform") {
		t.Errorf("requests %v, want only the market read on the skip path", *paths)
	}
}

// When credit is short, the topup buys the shortfall, never less than the
// node's amount and never less than Tendril's own minimum.
func TestTopupCoverBuysTheShortfall(t *testing.T) {
	cases := []struct {
		name     string
		rate     float64
		credit   int64
		amount   string
		minTopUp int64
		want     string
	}{
		// $40/hr x 0.25h = $10.00 needed, $1.00 held: short $9.00 > $2.
		{"shortfall above amount", 40, 1_000_000, "2", 100_000, "9000000"},
		// $6/hr x 0.25h = $1.50 needed, $1.00 held: short $0.50 < $2.
		{"amount above shortfall", 6, 1_000_000, "2", 100_000, "2000000"},
		// Short $0.001, rounded up to a cent, then lifted to Tendril's $0.10.
		{"tendril minimum", 6, 1_499_000, "0.01", 100_000, "100000"},
		// $1.23/hr x 0.25h = 307500 micros; 7500 held: short 300000 exactly.
		{"whole cents", 1.23, 7_500, "0.01", 10_000, "300000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, amount := coverMarket(t, tc.rate, tc.minTopUp)
			store := &fakeTendrilStore{tendrilCredit: tc.credit, agentMeshCredit: 100_000_000}
			_, err := executeTendrilTopup(context.Background(),
				models.WorkflowNode{TendrilAmount: tc.amount, TendrilCoverHours: "0.25"},
				TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1"})
			if err == nil || !strings.Contains(err.Error(), "did not settle") {
				t.Fatalf("err = %v, want the topup path's no-settlement error", err)
			}
			if *amount != tc.want {
				t.Errorf("topup amount = %q, want %q", *amount, tc.want)
			}
		})
	}
}

func TestTopupCoverFailsBeforePayingWithNoMachines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/topup" {
			t.Errorf("reached /topup with no machine online")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"nodes":[]}`))
	}))
	defer srv.Close()
	_, err := executeTendrilTopup(context.Background(),
		models.WorkflowNode{TendrilAmount: "2", TendrilCoverHours: "0.25"},
		TendrilConfig{Client: tendril.NewClient(srv.URL), Store: &fakeTendrilStore{agentMeshCredit: 10_000_000}, UserID: "user1"})
	if err == nil || !strings.Contains(err.Error(), "no machines are online") {
		t.Errorf("err = %v, want no machines online", err)
	}
}

func TestParseCoverHours(t *testing.T) {
	for in, want := range map[string]float64{"": 0, " ": 0, "0.25": 0.25, "24": 24} {
		if got, err := parseCoverHours(in); err != nil || got != want {
			t.Errorf("parseCoverHours(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"0", "-1", "abc", "25"} {
		if _, err := parseCoverHours(bad); err == nil {
			t.Errorf("parseCoverHours(%q) should have errored", bad)
		}
	}
}

func TestParseMinBalanceUSD(t *testing.T) {
	ok := map[string]float64{"": 0, "0": 0, "2": 2, " 1.5 ": 1.5}
	for in, want := range ok {
		got, err := parseMinBalanceUSD(in)
		if err != nil || got != want {
			t.Errorf("parseMinBalanceUSD(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"-1", "abc", "NaN", "Inf", "-Inf", "1e20"} {
		if _, err := parseMinBalanceUSD(bad); err == nil {
			t.Errorf("parseMinBalanceUSD(%q) should have errored", bad)
		}
	}
}

// Release is the only place compute is actually billed, so it must persist
// what Tendril reported rather than what AgentMesh predicted.
func TestReleaseLeasePersistsTendrilsOwnCharge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/x402/leases/lease_9k2m" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer plain-token" {
			t.Errorf("auth = %q, want the decrypted lease token", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"usedSeconds":1800,"chargedAtomic":3000000,"balance":9000000}`))
	}))
	defer srv.Close()

	enc, err := wallet.Encrypt("plain-token", testEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	store := &fakeTendrilStore{}
	res, _, err := ReleaseLease(context.Background(), TendrilConfig{
		Client: tendril.NewClient(srv.URL), Store: store, EncryptKey: testEncKey,
	}, models.TendrilLease{ID: "row1", LeaseID: "lease_9k2m", LeaseTokenEnc: enc})
	if err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if res.ChargedAtomic != 3_000_000 || res.UsedSeconds != 1800 {
		t.Errorf("result = %+v", res)
	}
	if store.releasedID != "row1" || store.releasedCharged != 3_000_000 || store.releasedSeconds != 1800 {
		t.Errorf("store got id=%q seconds=%d charged=%d",
			store.releasedID, store.releasedSeconds, store.releasedCharged)
	}
}

// Releasing twice must be harmless — the reaper and a user clicking Release can
// race, and a double DELETE against Tendril would surface as a run failure.
func TestReleaseLeaseIsIdempotentOnAlreadyReleased(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"lease not found"}`))
	}))
	defer srv.Close()

	enc, _ := wallet.Encrypt("plain-token", testEncKey)
	store := &fakeTendrilStore{}
	if _, _, err := ReleaseLease(context.Background(), TendrilConfig{
		Client: tendril.NewClient(srv.URL), Store: store, EncryptKey: testEncKey,
	}, models.TendrilLease{ID: "row1", LeaseID: "gone", LeaseTokenEnc: enc}); err != nil {
		t.Fatalf("ReleaseLease on a missing lease should not error: %v", err)
	}
	if store.releasedID != "row1" {
		t.Error("a lease Tendril no longer knows about must still be marked released locally")
	}
}

// Releasing early must hand the unused reservation back as TENDRIL credit —
// the hours stay the user's. Refunding to AgentMesh credit instead would let a
// user cycle rent/release to convert Tendril credit into general platform
// credit the pool cannot honour.
func TestReleaseRefundsUnusedReservationAsTendrilCredit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Reserved 2h at $6 (+1c gate) = 12.01; actually used 30 min = $3.00.
		w.Write([]byte(`{"usedSeconds":1800,"chargedAtomic":3000000,"balance":0}`))
	}))
	defer srv.Close()

	enc, _ := wallet.Encrypt("plain-token", testEncKey)
	store := &fakeTendrilStore{}
	_, refunded, err := ReleaseLease(context.Background(), TendrilConfig{
		Client: tendril.NewClient(srv.URL), Store: store, EncryptKey: testEncKey,
	}, models.TendrilLease{
		ID: "row1", UserID: "user1", LeaseID: "lease_9k2m", LeaseTokenEnc: enc,
		ReservedUSDMicros: 12_010_000,
	})
	if err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if refunded != 9_010_000 {
		t.Errorf("ReleaseLease's own refunded return = %d, want 9010000", refunded)
	}
	if store.refunded != 9_010_000 {
		t.Errorf("refunded = %d, want 9010000", store.refunded)
	}
}

// When Tendril charges MORE than a lease's own reservation, ReleaseLease
// must not refund anything (there is no unused amount) and must not silently
// absorb the overrun with no record of it -- see the alert.Notify call in
// the else-if branch this test exercises. This test only pins the refund
// side (nothing refunded, no panic/error on an overrun); the alert fires on
// a detached goroutine and isn't observable from here.
func TestReleaseDoesNotRefundOnOverrun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Reserved $3.00, but Tendril reports $5.00 charged -- a $2.00 overrun.
		w.Write([]byte(`{"usedSeconds":3000,"chargedAtomic":5000000,"balance":0}`))
	}))
	defer srv.Close()

	enc, _ := wallet.Encrypt("plain-token", testEncKey)
	store := &fakeTendrilStore{}
	res, refunded, err := ReleaseLease(context.Background(), TendrilConfig{
		Client: tendril.NewClient(srv.URL), Store: store, EncryptKey: testEncKey,
	}, models.TendrilLease{
		ID: "row1", UserID: "user1", LeaseID: "lease_overrun", LeaseTokenEnc: enc,
		ReservedUSDMicros: 3_000_000,
	})
	if err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if res.ChargedAtomic != 5_000_000 {
		t.Errorf("charged = %v, want 5000000 (the release must still report Tendril's real charge)", res.ChargedAtomic)
	}
	if refunded != 0 {
		t.Errorf("ReleaseLease's own refunded return = %d, want 0 -- an overrun is not an unused reservation", refunded)
	}
	if store.refunded != 0 {
		t.Errorf("refunded = %d, want 0 -- an overrun is not an unused reservation", store.refunded)
	}
}

// Two concurrent ReleaseLease calls against the same lease (a double-click,
// or the reaper racing a user) must refund exactly once, not twice. The
// fake store's MarkTendrilLeaseReleased returns transitioned = false on the
// second call, mirroring the real Store's WHERE status = 'active' guard --
// ReleaseLease must skip its refund/overrun accounting entirely when that
// happens, since the first caller already ran it against the same charge.
func TestReleaseIsNotDoubleRefundedOnConcurrentCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Reserved $12.01; used 30 min = $3.00 -- $9.01 unused.
		w.Write([]byte(`{"usedSeconds":1800,"chargedAtomic":3000000,"balance":0}`))
	}))
	defer srv.Close()

	enc, _ := wallet.Encrypt("plain-token", testEncKey)
	store := &fakeTendrilStore{}
	lease := models.TendrilLease{
		ID: "row1", UserID: "user1", LeaseID: "lease_race", LeaseTokenEnc: enc,
		ReservedUSDMicros: 12_010_000,
	}
	cfg := TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, EncryptKey: testEncKey}

	_, refunded1, err := ReleaseLease(context.Background(), cfg, lease)
	if err != nil {
		t.Fatalf("first ReleaseLease: %v", err)
	}
	if refunded1 != 9_010_000 {
		t.Fatalf("first ReleaseLease's own refunded return = %d, want 9010000", refunded1)
	}
	if store.refunded != 9_010_000 {
		t.Fatalf("refunded after first release = %d, want 9010000", store.refunded)
	}
	// Simulates the race: a second caller's Tendril DELETE also succeeds
	// (real Tendril behavior when the target isn't idempotent-safe against
	// a lease that's already fully released) with the SAME real charge.
	_, refunded2, err := ReleaseLease(context.Background(), cfg, lease)
	if err != nil {
		t.Fatalf("second ReleaseLease: %v", err)
	}
	if refunded2 != 0 {
		t.Errorf("second (racing) ReleaseLease's own refunded return = %d, want 0 -- must not double-refund", refunded2)
	}
	if store.refunded != 9_010_000 {
		t.Errorf("refunded after second (racing) release = %d, want 9010000 unchanged -- must not double-refund", store.refunded)
	}
}

// resolveLease's node.TendrilNodeID path must not let a run/release node
// act on another user's lease. node.TendrilNodeID is an AgentMesh lease row
// id set via PUT /workflows/{id}, accepted verbatim -- not restricted to
// what the Inspector's own machine picker would produce -- so this is a real
// cross-tenant boundary, not just defense in depth.
func TestResolveLeaseRejectsAnotherUsersLease(t *testing.T) {
	store := &fakeTendrilStore{
		byID: map[string]models.TendrilLease{
			"row_victim": {ID: "row_victim", UserID: "victim", LeaseID: "lease_victim", Status: "active"},
		},
	}
	_, err := resolveLease(context.Background(),
		models.WorkflowNode{TendrilNodeID: "row_victim"},
		TendrilConfig{Store: store, UserID: "attacker"})
	if err == nil {
		t.Fatal("want an error resolving another user's lease id, got nil")
	}
}

// The legitimate case: a node.TendrilNodeID naming the CALLING user's own
// lease must still resolve normally.
func TestResolveLeaseAllowsOwnLease(t *testing.T) {
	store := &fakeTendrilStore{
		byID: map[string]models.TendrilLease{
			"row_mine": {ID: "row_mine", UserID: "me", LeaseID: "lease_mine", Status: "active"},
		},
	}
	lease, err := resolveLease(context.Background(),
		models.WorkflowNode{TendrilNodeID: "row_mine"},
		TendrilConfig{Store: store, UserID: "me"})
	if err != nil {
		t.Fatalf("resolveLease: %v", err)
	}
	if lease.ID != "row_mine" {
		t.Errorf("resolved lease id = %q, want row_mine", lease.ID)
	}
}

type fakeTendrilStore struct {
	activeLeases    []models.TendrilLease
	activeLeaseErr  error
	hasLatestLease  bool
	latestLease     models.TendrilLease
	tendrilCredit   int64
	agentMeshCredit int64
	refunded        int64
	releasedID      string
	releasedSeconds int64
	releasedCharged int64
	inserted        models.TendrilLease
	byID            map[string]models.TendrilLease
	// alreadyReleased tracks every id MarkTendrilLeaseReleased has already
	// transitioned, so a second call against the same id can return
	// transitioned = false -- mirroring the real Store's
	// WHERE status = 'active' guard, needed to test ReleaseLease's
	// concurrent/double-release handling without a real database.
	alreadyReleased map[string]bool
}

func (f *fakeTendrilStore) InsertTendrilLease(_ context.Context, l models.TendrilLease) (models.TendrilLease, error) {
	l.ID = "row1"
	f.inserted = l
	return l, nil
}
func (f *fakeTendrilStore) GetTendrilLease(_ context.Context, id string) (models.TendrilLease, error) {
	return f.byID[id], nil
}
func (f *fakeTendrilStore) MarkTendrilLeaseReleased(_ context.Context, id string, used, charged int64) (bool, error) {
	if f.alreadyReleased == nil {
		f.alreadyReleased = map[string]bool{}
	}
	if f.alreadyReleased[id] {
		return false, nil
	}
	f.alreadyReleased[id] = true
	f.releasedID, f.releasedSeconds, f.releasedCharged = id, used, charged
	return true, nil
}
func (f *fakeTendrilStore) LatestActiveLeaseForRun(_ context.Context, _ string) (models.TendrilLease, error) {
	if f.hasLatestLease {
		return f.latestLease, nil
	}
	return models.TendrilLease{}, nil
}
func (f *fakeTendrilStore) LatestActiveLeaseForUser(_ context.Context, _ string) (models.TendrilLease, error) {
	if f.hasLatestLease {
		return f.latestLease, nil
	}
	return models.TendrilLease{}, nil
}
func (f *fakeTendrilStore) ListActiveTendrilLeases(_ context.Context, _ string) ([]models.TendrilLease, error) {
	if f.activeLeaseErr != nil || f.activeLeases != nil {
		return f.activeLeases, f.activeLeaseErr
	}
	if f.hasLatestLease {
		return []models.TendrilLease{f.latestLease}, nil
	}
	return nil, nil
}
func (f *fakeTendrilStore) TendrilCreditBalance(_ context.Context, _ string) (int64, error) {
	return f.tendrilCredit, nil
}
func (f *fakeTendrilStore) CreditBalance(_ context.Context, _ string) (int64, error) {
	return f.agentMeshCredit, nil
}

// Credit-only, matching the real Store.ConvertCreditsToTendril: the caller's
// x402 relay call already billed AgentMesh credit for the real settlement
// before this is ever invoked, so this must not also touch agentMeshCredit
// (see that function's doc comment for the double-debit bug this mirrors
// the fix for).
func (f *fakeTendrilStore) ConvertCreditsToTendril(_ context.Context, _ string, amount int64, _ string) (int64, error) {
	f.tendrilCredit += amount
	return f.tendrilCredit, nil
}
func (f *fakeTendrilStore) ChargeTendrilCredit(_ context.Context, _, leaseID, kind string, amount int64) error {
	if kind == "refund" {
		f.tendrilCredit += amount
		f.refunded += amount
		return nil
	}
	f.tendrilCredit -= amount
	return nil
}

func TestParseAutoBudgetUSD(t *testing.T) {
	ok := map[string]float64{"": autoRentDefaultBudgetUSD, "1": 1, "2.5": 2.5, " 5 ": 5}
	for in, want := range ok {
		got, err := parseAutoBudgetUSD(in)
		if err != nil {
			t.Errorf("parseAutoBudgetUSD(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseAutoBudgetUSD(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"0", "-1", "abc"} {
		if _, err := parseAutoBudgetUSD(bad); err == nil {
			t.Errorf("parseAutoBudgetUSD(%q) should have errored", bad)
		}
	}
}

// An auto node with an already-active, still-funded lease must reuse it --
// no market lookup, no reservation, no rent -- and run the payload straight
// against that lease's own token.
func TestAutoReusesActiveLeaseWithoutRenting(t *testing.T) {
	for _, machineID := range []string{"", "m1"} {
		t.Run("machine="+machineID, func(t *testing.T) {
			var hits []struct{ path, auth string }
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits = append(hits, struct{ path, auth string }{r.URL.Path, r.Header.Get("Authorization")})
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			enc, err := wallet.Encrypt("plain-token", testEncKey)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			store := &fakeTendrilStore{
				hasLatestLease: true,
				latestLease: models.TendrilLease{
					ID: "row1", UserID: "user1", LeaseID: "lease1", Status: "active", TendrilNodeID: "m1",
					LeaseTokenEnc: enc, FundedUntil: time.Now().Add(time.Hour),
					StartedAt: time.Now().Add(-15 * time.Minute), HoursPurchased: 1,
				},
			}
			node := models.WorkflowNode{
				TendrilAction: "auto", TendrilNodeID: machineID,
				CustomParams: []models.CustomParam{{Name: "payload", Value: "print(1)"}},
			}
			cfg := TendrilConfig{
				Client: tendril.NewClient(srv.URL), Store: store, EncryptKey: testEncKey,
				UserID: "user1", RunID: "run1",
			}
			if _, err := executeTendrilAuto(context.Background(), node, emptyRunContext{}, cfg); err != nil {
				t.Fatalf("executeTendrilAuto: %v", err)
			}
			if len(hits) != 1 || hits[0].path != "/x402/run" {
				t.Fatalf("hits = %+v, want exactly one call to /x402/run (no market lookup, no rent)", hits)
			}
			if hits[0].auth != "Bearer plain-token" {
				t.Errorf("auth = %q, want the reused lease's own decrypted token", hits[0].auth)
			}
			if store.tendrilCredit != 0 {
				t.Errorf("tendril credit = %d, want 0 -- reusing a lease must not touch it", store.tendrilCredit)
			}

		})
	}
}

// With no active lease and no explicit budget, an auto node must fall back
// to autoRentDefaultBudgetUSD and auto-fund the shortfall before renting --
// this pins the $2/hr * 0.5h = $1 arithmetic issue #173's "one dollar" ask
// describes, by asserting the exact topup amount it computes and requests.
func TestAutoRentsWithDefaultBudgetWhenNoActiveLease(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/platform":
			w.Write([]byte(`{}`))
		case "/explorer":
			w.Write([]byte(`{"nodes":[{"id":"m1","label":"M1","cpuCores":2,"ramMb":2048,"pricePerHourUsd":2,"status":"online"}]}`))
		default:
			// Not a 402 -- no payment challenge was ever issued, so
			// performTopup must refuse to mint credit (see
			// TestTopupRefusesToCreditWithoutRealSettlement). Good enough to
			// prove auto reached the topup call with the right amount.
			w.Write([]byte(`{"status":"degraded"}`))
		}
	}))
	defer srv.Close()

	store := &fakeTendrilStore{agentMeshCredit: 10_000_000}
	node := models.WorkflowNode{
		TendrilAction: "auto",
		CustomParams:  []models.CustomParam{{Name: "payload", Value: "print(1)"}},
	}
	cfg := TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1", RunID: "run1"}
	_, err := executeTendrilAuto(context.Background(), node, emptyRunContext{}, cfg)
	if err == nil || !strings.Contains(err.Error(), "auto topup") {
		t.Fatalf("executeTendrilAuto error = %v, want a wrapped auto-topup error (no 402 was ever offered)", err)
	}
	// $1 default budget / $2/hr machine = 0.5h; RequiredCreditAtomic(2_000_000, 0.5) = 1_000_000.
	wantAmount := "amount=1000000"
	found := false
	for _, p := range paths {
		if p == "/topup?"+wantAmount {
			found = true
		}
	}
	if !found {
		t.Errorf("requested paths = %v, want a /topup call with %s", paths, wantAmount)
	}
}

// A budget that would buy more than maxTendrilHours on a cheap machine must
// be capped, not used to rent a multi-day lease by accident.
func TestAutoCapsHoursAtMax(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/platform":
			w.Write([]byte(`{}`))
		case "/explorer":
			w.Write([]byte(`{"nodes":[{"id":"m1","label":"M1","cpuCores":1,"ramMb":512,"pricePerHourUsd":0.01,"status":"online"}]}`))
		default:
			w.Write([]byte(`{"status":"degraded"}`))
		}
	}))
	defer srv.Close()

	store := &fakeTendrilStore{agentMeshCredit: 10_000_000}
	node := models.WorkflowNode{
		TendrilAction: "auto",
		CustomParams:  []models.CustomParam{{Name: "payload", Value: "print(1)"}},
	}
	cfg := TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1", RunID: "run1"}
	_, _ = executeTendrilAuto(context.Background(), node, emptyRunContext{}, cfg)
	// $1 budget / $0.01/hr would be 100h uncapped; capped at 24h:
	// RequiredCreditAtomic(10_000, 24) = 240_000.
	wantAmount := "amount=240000"
	found := false
	for _, p := range paths {
		if p == "/topup?"+wantAmount {
			found = true
		}
	}
	if !found {
		t.Errorf("requested paths = %v, want a /topup call with %s (hours capped at %v)", paths, wantAmount, maxTendrilHours)
	}
}

func TestAutoErrorsWhenNoMachinesOnline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"nodes":[]}`))
	}))
	defer srv.Close()

	store := &fakeTendrilStore{agentMeshCredit: 10_000_000}
	node := models.WorkflowNode{
		TendrilAction: "auto",
		CustomParams:  []models.CustomParam{{Name: "payload", Value: "print(1)"}},
	}
	cfg := TendrilConfig{Client: tendril.NewClient(srv.URL), Store: store, UserID: "user1", RunID: "run1"}
	if _, err := executeTendrilAuto(context.Background(), node, emptyRunContext{}, cfg); err == nil {
		t.Fatal("want an error when no machines are online")
	}
}

func TestAutoRequiresPayload(t *testing.T) {
	store := &fakeTendrilStore{}
	node := models.WorkflowNode{TendrilAction: "auto"}
	cfg := TendrilConfig{Store: store, UserID: "user1", RunID: "run1"}
	if _, err := executeTendrilAuto(context.Background(), node, emptyRunContext{}, cfg); err == nil {
		t.Fatal("want an error when the auto node has no payload")
	}
}
