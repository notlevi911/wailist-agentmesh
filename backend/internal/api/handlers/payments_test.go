package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agentmesh/backend/internal/api/handlers"
	"github.com/agentmesh/backend/internal/payments"
)

// fakeCashfree satisfies the CashfreeClient interface for unit tests.
type fakeCashfree struct {
	order       payments.CashfreeOrder
	createErr   error
	statusValue string
	statusErr   error
	sigValid    bool
	gotPhone    string // records the phone CreateOrder was actually called with
}

func (f *fakeCashfree) CreateOrder(_ context.Context, _ int64, orderID, _, _, phone string) (payments.CashfreeOrder, error) {
	f.gotPhone = phone
	if f.createErr != nil {
		return payments.CashfreeOrder{}, f.createErr
	}
	o := f.order
	if o.OrderID == "" {
		o.OrderID = orderID
	}
	return o, nil
}

func (f *fakeCashfree) GetOrderStatus(_ context.Context, _ string) (string, error) {
	return f.statusValue, f.statusErr
}

func (f *fakeCashfree) VerifyWebhookSignature(_ []byte, _, _ string) bool {
	return f.sigValid
}

func TestCreateCashfreeOrderRejectsBelowMinimum(t *testing.T) {
	d := &handlers.Deps{Cashfree: &fakeCashfree{}}
	body, _ := json.Marshal(map[string]int64{"amount_inr_paise": 50})
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/order", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "user1"))
	w := httptest.NewRecorder()

	d.CreateCashfreeOrder(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestCreateCashfreeOrderRejectsAboveMaximum(t *testing.T) {
	d := &handlers.Deps{Cashfree: &fakeCashfree{}}
	body, _ := json.Marshal(map[string]int64{"amount_inr_paise": 5_00_000_01})
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/order", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "user1"))
	w := httptest.NewRecorder()

	d.CreateCashfreeOrder(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestVerifyCashfreePaymentRejectsMissingOrderID(t *testing.T) {
	d := &handlers.Deps{Cashfree: &fakeCashfree{statusValue: "PAID"}}
	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/verify", bytes.NewReader(body))
	w := httptest.NewRecorder()

	d.VerifyCashfreePayment(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestVerifyCashfreePaymentRejectsUnpaidStatus(t *testing.T) {
	d := &handlers.Deps{Cashfree: &fakeCashfree{statusValue: "ACTIVE"}}
	body, _ := json.Marshal(map[string]string{"order_id": "order_1"})
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/verify", bytes.NewReader(body))
	w := httptest.NewRecorder()

	d.VerifyCashfreePayment(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402 got %d", w.Code)
	}
}

func TestCashfreeWebhookRejectsMissingSignatureHeader(t *testing.T) {
	d := &handlers.Deps{Cashfree: &fakeCashfree{sigValid: true}}
	body := []byte(`{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{"order":{"order_id":"x"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/webhook", bytes.NewReader(body))
	// No x-webhook-signature header
	w := httptest.NewRecorder()

	d.CashfreeWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestCashfreeWebhookRejectsBadSignature(t *testing.T) {
	d := &handlers.Deps{Cashfree: &fakeCashfree{sigValid: false}}
	body := []byte(`{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{"order":{"order_id":"x"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/webhook", bytes.NewReader(body))
	req.Header.Set("x-webhook-signature", "bad")
	req.Header.Set("x-webhook-timestamp", "1234567890")
	w := httptest.NewRecorder()

	d.CashfreeWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestCashfreeWebhookIgnoresUnknownEventType(t *testing.T) {
	d := &handlers.Deps{Cashfree: &fakeCashfree{sigValid: true}}
	body := []byte(`{"type":"ORDER_CREATED_WEBHOOK","data":{"order":{"order_id":"x"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/webhook", bytes.NewReader(body))
	req.Header.Set("x-webhook-signature", "valid")
	req.Header.Set("x-webhook-timestamp", "1234567890")
	w := httptest.NewRecorder()

	d.CashfreeWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
}

func TestCashfreeWebhookRejectsUnknownOrder(t *testing.T) {
	base := testDeps(t)
	d := &handlers.Deps{Store: base.Store, Cashfree: &fakeCashfree{sigValid: true}}

	orderID := fmt.Sprintf("order_unknown_%d", time.Now().UnixNano())
	body := []byte(fmt.Sprintf(`{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{"order":{"order_id":"%s"},"payment":{"cf_payment_id":"pay_1"}}}`, orderID))
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/webhook", bytes.NewReader(body))
	req.Header.Set("x-webhook-signature", "valid")
	req.Header.Set("x-webhook-timestamp", "1234567890")
	w := httptest.NewRecorder()

	d.CashfreeWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestVerifyCashfreePaymentRejectsUnknownOrder(t *testing.T) {
	base := testDeps(t)
	d := &handlers.Deps{Store: base.Store, Cashfree: &fakeCashfree{statusValue: "PAID"}}

	orderID := fmt.Sprintf("order_unknown_%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]string{"order_id": orderID})
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/verify", bytes.NewReader(body))
	w := httptest.NewRecorder()

	d.VerifyCashfreePayment(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestGetCreditBalanceReturnsStoredBalance(t *testing.T) {
	d := testDeps(t)

	email := fmt.Sprintf("balance-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("order_balance_%d", time.Now().UnixNano())
	if _, err := d.Store.CreateCreditTransaction(context.Background(), user.ID, orderID, 10000, 0.012); err != nil {
		t.Fatal(err)
	}
	wantMicros, _, err := d.Store.CompleteCreditTransaction(context.Background(), "cashfree", orderID, "pay_balance_test")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/credits/balance", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, user.ID))
	w := httptest.NewRecorder()

	d.GetCreditBalance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
	var resp struct {
		CreditUSDMicros int64 `json:"credit_usd_micros"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.CreditUSDMicros != wantMicros {
		t.Fatalf("want %d got %d", wantMicros, resp.CreditUSDMicros)
	}
}

func redeemCoupon(t *testing.T, d *handlers.Deps, userID, code string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequest(http.MethodPost, "/credits/redeem-coupon", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, userID))
	w := httptest.NewRecorder()

	d.RedeemCoupon(w, req)

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return w.Code, resp
}

// Codes come from configuration now (COUPON_CODES), so these tests install
// their own catalog rather than depending on a hardcoded one.
func withTestCoupons(d *handlers.Deps) {
	d.Store.SetCouponCatalog(map[string]int64{
		"TESTCODEA": 5_000_000,
		"TESTCODEB": 5_000_000,
	})
}

func TestRedeemCouponCreditsBalanceOnce(t *testing.T) {
	d := testDeps(t)
	withTestCoupons(d)
	email := fmt.Sprintf("coupon-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	code, status := redeemCoupon(t, d, user.ID, "TESTCODEA")
	if code != http.StatusOK {
		t.Fatalf("want 200 got %d (%v)", code, status)
	}
	if got := status["credit_usd_micros"].(float64); got != 5_000_000 {
		t.Fatalf("want balance 5000000 got %v", got)
	}
	if got := status["credited_usd_micros"].(float64); got != 5_000_000 {
		t.Fatalf("want credited 5000000 got %v", got)
	}

	// Same code again: rejected, balance unchanged.
	code, status = redeemCoupon(t, d, user.ID, "TESTCODEA")
	if code != http.StatusConflict {
		t.Fatalf("want 409 got %d (%v)", code, status)
	}
	balance, err := d.Store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 5_000_000 {
		t.Fatalf("want balance still 5000000 got %d", balance)
	}
}

func TestRedeemCouponStacksAcrossDistinctCodes(t *testing.T) {
	d := testDeps(t)
	withTestCoupons(d)
	email := fmt.Sprintf("coupon-stack-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	if code, status := redeemCoupon(t, d, user.ID, "TESTCODEA"); code != http.StatusOK {
		t.Fatalf("want 200 got %d (%v)", code, status)
	}
	code, status := redeemCoupon(t, d, user.ID, "TESTCODEB")
	if code != http.StatusOK {
		t.Fatalf("want 200 got %d (%v)", code, status)
	}
	if got := status["credit_usd_micros"].(float64); got != 10_000_000 {
		t.Fatalf("want stacked balance 10000000 got %v", got)
	}
}

func TestRedeemCouponRejectsUnknownCode(t *testing.T) {
	d := testDeps(t)
	withTestCoupons(d)
	email := fmt.Sprintf("coupon-invalid-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	code, status := redeemCoupon(t, d, user.ID, "NOTAREALCODE")
	if code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d (%v)", code, status)
	}
}

// An unconfigured catalog (COUPON_CODES unset) must reject everything rather
// than falling back to any built-in code.
func TestRedeemCouponRejectsEveryCodeWhenCatalogEmpty(t *testing.T) {
	d := testDeps(t)
	d.Store.SetCouponCatalog(nil)
	email := fmt.Sprintf("coupon-empty-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	if code, status := redeemCoupon(t, d, user.ID, "TESTCODEA"); code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d (%v)", code, status)
	}
}

// --- NOWPayments tests (unchanged) ---

type fakeNOWPayments struct {
	invoice   payments.Invoice
	createErr error
	sigValid  bool
}

func (f *fakeNOWPayments) CreateInvoice(ctx context.Context, amountUSDCents int64, orderID, ipnCallbackURL, successURL, cancelURL string) (payments.Invoice, error) {
	if f.createErr != nil {
		return payments.Invoice{}, f.createErr
	}
	return f.invoice, nil
}

func (f *fakeNOWPayments) VerifyIPNSignature(body []byte, signature string) bool {
	return f.sigValid
}

func TestCreateCryptoInvoiceHappyPath(t *testing.T) {
	base := testDeps(t)
	d := &handlers.Deps{
		Store:       base.Store,
		NOWPayments: &fakeNOWPayments{invoice: payments.Invoice{ID: "inv_1", InvoiceURL: "https://nowpayments.io/payment/inv_1"}},
	}

	email := fmt.Sprintf("crypto-invoice-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]int64{"amount_usd_cents": 1999})
	req := httptest.NewRequest(http.MethodPost, "/payments/nowpayments/invoice", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, user.ID))
	w := httptest.NewRecorder()

	d.CreateCryptoInvoice(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		OrderID    string `json:"order_id"`
		InvoiceURL string `json:"invoice_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.InvoiceURL != "https://nowpayments.io/payment/inv_1" || resp.OrderID == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestCreateCryptoInvoiceRejectsBelowMinimum(t *testing.T) {
	d := &handlers.Deps{NOWPayments: &fakeNOWPayments{}}
	body, _ := json.Marshal(map[string]int64{"amount_usd_cents": 50})
	req := httptest.NewRequest(http.MethodPost, "/payments/nowpayments/invoice", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "user1"))
	w := httptest.NewRecorder()

	d.CreateCryptoInvoice(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestCreateCryptoInvoiceLeavesOrphanedPendingRowOnInvoiceFailure(t *testing.T) {
	base := testDeps(t)
	d := &handlers.Deps{
		Store:       base.Store,
		NOWPayments: &fakeNOWPayments{createErr: fmt.Errorf("nowpayments: simulated outage")},
	}

	email := fmt.Sprintf("crypto-invoice-orphan-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]int64{"amount_usd_cents": 1999})
	req := httptest.NewRequest(http.MethodPost, "/payments/nowpayments/invoice", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, user.ID))
	w := httptest.NewRecorder()

	d.CreateCryptoInvoice(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}

	url := os.Getenv("TEST_DATABASE_URL")
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM credit_ledger WHERE user_id = $1 AND provider = 'nowpayments' AND status = 'pending'`,
		user.ID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want exactly 1 orphaned pending ledger row for this user, got %d", count)
	}
}

func TestNOWPaymentsWebhookRejectsBadSignature(t *testing.T) {
	d := &handlers.Deps{NOWPayments: &fakeNOWPayments{sigValid: false}}
	req := httptest.NewRequest(http.MethodPost, "/payments/nowpayments/webhook", strings.NewReader(`{"order_id":"x","payment_status":"finished","payment_id":1}`))
	req.Header.Set("x-nowpayments-sig", "bad-sig")
	w := httptest.NewRecorder()

	d.NOWPaymentsWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestNOWPaymentsWebhookCreditsOnFinished(t *testing.T) {
	base := testDeps(t)
	d := &handlers.Deps{Store: base.Store, NOWPayments: &fakeNOWPayments{sigValid: true}}

	email := fmt.Sprintf("crypto-webhook-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("order_wh_%d", time.Now().UnixNano())
	if _, err := d.Store.CreateCryptoCreditTransaction(context.Background(), user.ID, "nowpayments", orderID, 1999); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"order_id":"%s","payment_status":"finished","payment_id":6084744717}`, orderID)
	req := httptest.NewRequest(http.MethodPost, "/payments/nowpayments/webhook", strings.NewReader(body))
	req.Header.Set("x-nowpayments-sig", "sig-does-not-matter-fake-always-valid")
	w := httptest.NewRecorder()

	d.NOWPaymentsWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	balance, err := d.Store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 1999*10_000 {
		t.Fatalf("want balance %d got %d", 1999*10_000, balance)
	}
}

func TestNOWPaymentsWebhookDoesNotCreditOnConfirmed(t *testing.T) {
	base := testDeps(t)
	d := &handlers.Deps{Store: base.Store, NOWPayments: &fakeNOWPayments{sigValid: true}}

	email := fmt.Sprintf("crypto-webhook-confirmed-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("order_wh_confirmed_%d", time.Now().UnixNano())
	if _, err := d.Store.CreateCryptoCreditTransaction(context.Background(), user.ID, "nowpayments", orderID, 1999); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"order_id":"%s","payment_status":"confirmed","payment_id":1}`, orderID)
	req := httptest.NewRequest(http.MethodPost, "/payments/nowpayments/webhook", strings.NewReader(body))
	req.Header.Set("x-nowpayments-sig", "sig-does-not-matter-fake-always-valid")
	w := httptest.NewRecorder()

	d.NOWPaymentsWebhook(w, req)

	balance, err := d.Store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("want balance untouched at 0 on mere 'confirmed', got %d", balance)
	}
}

func TestNOWPaymentsWebhookMarksPartialWithoutCrediting(t *testing.T) {
	base := testDeps(t)
	d := &handlers.Deps{Store: base.Store, NOWPayments: &fakeNOWPayments{sigValid: true}}

	email := fmt.Sprintf("crypto-webhook-partial-test-%d@example.com", time.Now().UnixNano())
	user, err := d.Store.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	orderID := fmt.Sprintf("order_wh_partial_%d", time.Now().UnixNano())
	if _, err := d.Store.CreateCryptoCreditTransaction(context.Background(), user.ID, "nowpayments", orderID, 1999); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`{"order_id":"%s","payment_status":"partially_paid","payment_id":1}`, orderID)
	req := httptest.NewRequest(http.MethodPost, "/payments/nowpayments/webhook", strings.NewReader(body))
	req.Header.Set("x-nowpayments-sig", "sig-does-not-matter-fake-always-valid")
	w := httptest.NewRecorder()

	d.NOWPaymentsWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	balance, err := d.Store.GetCreditBalance(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("want balance untouched at 0 pending manual reconciliation, got %d", balance)
	}
}

// TestCreateCashfreeOrderRejectsMissingPhone pins the fix for a real bug: an
// order used to be created with a hardcoded "9999999999" whenever the
// request carried no phone, silently sending a fake number to Cashfree and
// showing it back to a real paying customer on their receipt. A missing or
// malformed phone must now fail the request instead of substituting one.
func TestCreateCashfreeOrderRejectsMissingPhone(t *testing.T) {
	d := &handlers.Deps{Cashfree: &fakeCashfree{}}
	body, _ := json.Marshal(map[string]any{"amount_inr_paise": 10000})
	req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/order", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "user1"))
	w := httptest.NewRecorder()

	d.CreateCashfreeOrder(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a missing phone, got %d", w.Code)
	}
}

// TestCreateCashfreeOrderRejectsMalformedPhone covers a phone that is
// present but not a real 10-digit Indian mobile number — too short, or
// starting with a digit TRAI never assigns to mobiles (0-5).
func TestCreateCashfreeOrderRejectsMalformedPhone(t *testing.T) {
	for _, phone := range []string{"12345", "123456789", "0123456789", "notaphone"} {
		d := &handlers.Deps{Cashfree: &fakeCashfree{}}
		body, _ := json.Marshal(map[string]any{"amount_inr_paise": 10000, "phone": phone})
		req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/order", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, "user1"))
		w := httptest.NewRecorder()

		d.CreateCashfreeOrder(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("phone %q: want 400, got %d", phone, w.Code)
		}
	}
}

// TestCreateCashfreeOrderAcceptsRealPhoneFormats confirms the common ways a
// user actually types or pastes a number all normalize to the same 10 digits
// Cashfree receives — plain, with a "+91" country code, with a bare "91",
// and with spaces/hyphens as a real keypad or paste would produce.
func TestCreateCashfreeOrderAcceptsRealPhoneFormats(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	payments.SetFetchRateForTest(func(context.Context) (float64, error) { return 0.012, nil })
	defer payments.SetFetchRateForTest(nil)

	for _, input := range []string{"9876543210", "+91 98765 43210", "919876543210", "98765-43210"} {
		d := testDeps(t)
		fake := &fakeCashfree{order: payments.CashfreeOrder{PaymentSessionID: "session_123"}}
		d.Cashfree = fake

		email := fmt.Sprintf("phone-test-%d@example.com", time.Now().UnixNano())
		user, err := d.Store.CreateUser(context.Background(), email, "hash")
		if err != nil {
			t.Fatal(err)
		}

		body, _ := json.Marshal(map[string]any{"amount_inr_paise": 10000, "phone": input})
		req := httptest.NewRequest(http.MethodPost, "/payments/cashfree/order", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), handlers.CtxUserID, user.ID))
		w := httptest.NewRecorder()

		d.CreateCashfreeOrder(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("phone %q: want 201, got %d (%s)", input, w.Code, w.Body.String())
		}
		if fake.gotPhone != "9876543210" {
			t.Fatalf("phone %q: want normalized to 9876543210, got %q", input, fake.gotPhone)
		}
	}
}
