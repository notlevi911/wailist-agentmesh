package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/agentmesh/backend/internal/alert"
	"github.com/agentmesh/backend/internal/db"
	"github.com/agentmesh/backend/internal/payments"
	"github.com/agentmesh/backend/internal/respond"
)

const (
	minCashfreeAmountPaise = 100
	// 5,00,000 INR cap — well above any real top-up preset, guards against
	// fat-fingered or abusive amounts.
	maxCashfreeAmountPaise = 5_00_000_00

	minCryptoAmountUSDCents = 100     // $1
	maxCryptoAmountUSDCents = 600_000 // $6,000
)

// indianPhonePattern matches a bare 10-digit Indian mobile number (starts
// 6-9, per TRAI's numbering plan) after normalizeIndianPhone has stripped
// formatting and any country code.
var indianPhonePattern = regexp.MustCompile(`^[6-9]\d{9}$`)

// normalizeIndianPhone validates and normalizes a customer-entered phone
// number to the bare 10-digit form Cashfree's customer_phone expects.
// Accepts common variations a user might type or paste: spaces, hyphens,
// a leading "+91", "91", or "0". Anything else is rejected outright rather
// than passed through — Cashfree's own placeholder ("9999999999") was
// silently accepted and shown back to real customers on their receipt,
// which is the bug this replaces; a malformed number reaching Cashfree
// unnoticed would be the same failure in a new shape.
func normalizeIndianPhone(raw string) (string, error) {
	digits := regexp.MustCompile(`\D`).ReplaceAllString(raw, "")
	digits = strings.TrimPrefix(digits, "0")
	if len(digits) == 12 && strings.HasPrefix(digits, "91") {
		digits = digits[2:]
	}
	if !indianPhonePattern.MatchString(digits) {
		return "", fmt.Errorf("invalid phone number")
	}
	return digits, nil
}

func (d *Deps) CreateCashfreeOrder(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	var body struct {
		AmountINRPaise int64  `json:"amount_inr_paise"`
		Phone          string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.AmountINRPaise < minCashfreeAmountPaise {
		respond.Error(w, http.StatusBadRequest, "amount must be at least 100 paise")
		return
	}
	if body.AmountINRPaise > maxCashfreeAmountPaise {
		respond.Error(w, http.StatusBadRequest, "amount exceeds maximum allowed")
		return
	}
	phone, err := normalizeIndianPhone(body.Phone)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "enter a valid 10-digit phone number")
		return
	}

	rate, err := payments.FetchINRToUSDRate(r.Context())
	if err != nil {
		log.Printf("cashfree order: fx rate: %v", err)
		go alert.Notify(context.Background(), alert.ChannelPayments, fmt.Sprintf("FX rate fetch failing, top-ups are down: %v", err))
		respond.Error(w, http.StatusBadGateway, "could not fetch exchange rate")
		return
	}

	user, err := d.Store.GetUserByID(r.Context(), userID)
	if err != nil {
		log.Printf("cashfree order: get user: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	orderID := uuid.New().String()

	// Create the ledger row before calling Cashfree so a Cashfree failure
	// leaves a harmless dead pending row rather than a real payable order
	// with no ledger row to complete.
	if _, err := d.Store.CreateCreditTransaction(r.Context(), userID, orderID, body.AmountINRPaise, rate); err != nil {
		log.Printf("cashfree order: create ledger row: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	order, err := d.Cashfree.CreateOrder(r.Context(), body.AmountINRPaise, orderID, userID, user.Email, phone)
	if err != nil {
		log.Printf("cashfree order: create order: %v", err)
		respond.Error(w, http.StatusBadGateway, "cashfree order creation failed")
		return
	}

	respond.JSON(w, http.StatusCreated, map[string]any{
		"order_id":           order.OrderID,
		"payment_session_id": order.PaymentSessionID,
		"amount":             body.AmountINRPaise,
		"currency":           "INR",
		"app_id":             d.CashfreeAppID,
	})
}

func (d *Deps) GetCreditBalance(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	balance, err := d.Store.GetCreditBalance(r.Context(), userID)
	if err != nil {
		log.Printf("credit balance: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{"credit_usd_micros": balance})
}

// creditPurchasesDefaultLimit caps an unparameterised billing-history read.
// Deep history is a rare ask; the page shows a reverse-chronological list and
// a user looking for one old receipt is better served by a future date filter
// than by every client paying to transfer a whole ledger on page load.
const creditPurchasesDefaultLimit = 50

// creditPurchasesMaxLimit bounds ?limit= so a caller cannot ask for an
// unbounded scan of their own ledger.
const creditPurchasesMaxLimit = 200

// GetCreditPurchases returns the signed-in user's top-up history from
// credit_ledger — the authoritative record, replacing the per-browser
// localStorage copy the billing page used to keep (see
// db.ListCreditTransactions for why that had to change).
func (d *Deps) GetCreditPurchases(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	limit := creditPurchasesDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		// Mirrors UsageSettlements' guard: a negative value parses fine but
		// would reach the query (and make([]T, 0, v)) as a nonsense capacity.
		if err != nil || v < 1 {
			respond.Error(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		if v > creditPurchasesMaxLimit {
			v = creditPurchasesMaxLimit
		}
		limit = v
	}

	txns, err := d.Store.ListCreditTransactions(r.Context(), userID, limit)
	if err != nil {
		log.Printf("credit purchases: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	respond.JSON(w, http.StatusOK, txns)
}

// RedeemCoupon credits a signed-in user's balance for a known, unredeemed
// coupon code. Each code is redeemable once per user (db.RedeemCoupon
// enforces this via a unique constraint) — a repeat or unknown code is a 4xx,
// not a transient failure, so callers shouldn't retry it.
func (d *Deps) RedeemCoupon(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Code) == "" {
		respond.Error(w, http.StatusBadRequest, "missing code")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body.Code))

	balance, credited, err := d.Store.RedeemCoupon(r.Context(), userID, code)
	switch {
	case errors.Is(err, db.ErrCouponInvalid):
		respond.Error(w, http.StatusBadRequest, "invalid coupon code")
		return
	case errors.Is(err, db.ErrCouponAlreadyRedeemed):
		respond.Error(w, http.StatusConflict, "coupon already redeemed")
		return
	case err != nil:
		log.Printf("redeem coupon: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"credit_usd_micros": balance,
		// What this specific code granted, so the UI can report the amount added
		// instead of assuming a fixed one — coupon values are configuration now.
		"credited_usd_micros": credited,
	})
}

// VerifyCashfreePayment is called by the frontend after the Cashfree JS SDK
// reports payment completion. It fetches the order status from Cashfree's API
// (server-to-server, so it cannot be spoofed) and credits the user if PAID.
func (d *Deps) VerifyCashfreePayment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderID string `json:"order_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OrderID == "" {
		respond.Error(w, http.StatusBadRequest, "missing order_id")
		return
	}

	status, err := d.Cashfree.GetOrderStatus(r.Context(), body.OrderID)
	if err != nil {
		log.Printf("cashfree verify: get order status: %v", err)
		respond.Error(w, http.StatusBadGateway, "could not verify payment with cashfree")
		return
	}
	if status != "PAID" {
		respond.Error(w, http.StatusPaymentRequired, "payment not completed")
		return
	}

	creditedMicros, applied, err := d.Store.CompleteCreditTransaction(r.Context(), "cashfree", body.OrderID, body.OrderID)
	if errors.Is(err, db.ErrCreditTransactionNotFound) {
		respond.Error(w, http.StatusBadRequest, "unknown order")
		return
	}
	if err != nil {
		log.Printf("cashfree verify: complete transaction: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if applied {
		go alert.Notify(context.Background(), alert.ChannelCredits, fmt.Sprintf("credited $%.2f (order %s, via cashfree)", float64(creditedMicros)/1e6, body.OrderID))
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"status":              "credited",
		"credited_usd_micros": creditedMicros,
	})
}

// CashfreeWebhook is the server-side backstop for CreateCashfreeOrder /
// VerifyCashfreePayment: if a client-side verify call never lands (dropped
// connection, closed tab) after Cashfree actually captures a payment, this
// webhook independently completes the same ledger row.
// CompleteCreditTransaction is idempotent, so it is safe to call from both
// this webhook and the client verify path for the same order.
//
// Public, unauthenticated route — Cashfree's servers call it directly,
// authenticated by the HMAC-SHA256 signature in the x-webhook-signature
// header, verified against the webhook secret configured in Cashfree's
// dashboard.
func (d *Deps) CashfreeWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "could not read body")
		return
	}

	signature := r.Header.Get("x-webhook-signature")
	timestamp := r.Header.Get("x-webhook-timestamp")
	if signature == "" || timestamp == "" || !d.Cashfree.VerifyWebhookSignature(body, signature, timestamp) {
		log.Printf("cashfree webhook: rejected signature from %s", r.RemoteAddr)
		go alert.Notify(context.Background(), alert.ChannelPayments, fmt.Sprintf("rejected cashfree webhook signature from %s", r.RemoteAddr))
		respond.Error(w, http.StatusBadRequest, "signature verification failed")
		return
	}

	var event struct {
		Type string `json:"type"`
		Data struct {
			Order struct {
				OrderID     string `json:"order_id"`
				OrderStatus string `json:"order_status"`
			} `json:"order"`
			Payment struct {
				PaymentID     string `json:"cf_payment_id"`
				PaymentStatus string `json:"payment_status"`
			} `json:"payment"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}

	orderID := event.Data.Order.OrderID
	if orderID == "" {
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	switch event.Type {
	case "PAYMENT_SUCCESS_WEBHOOK":
		paymentID := event.Data.Payment.PaymentID
		creditedMicros, applied, err := d.Store.CompleteCreditTransaction(r.Context(), "cashfree", orderID, paymentID)
		if err != nil {
			if errors.Is(err, db.ErrCreditTransactionNotFound) {
				log.Printf("cashfree webhook: unknown order_id %s (payment %s)", orderID, paymentID)
				respond.Error(w, http.StatusBadRequest, "unknown order")
				return
			}
			log.Printf("cashfree webhook: complete transaction: %v", err)
			go alert.Notify(context.Background(), alert.ChannelPayments, fmt.Sprintf("failed to complete order %s: %v", orderID, err))
			respond.Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if applied {
			go alert.Notify(context.Background(), alert.ChannelCredits, fmt.Sprintf("credited $%.2f (order %s, payment %s, via cashfree webhook)", float64(creditedMicros)/1e6, orderID, paymentID))
		}
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})

	case "PAYMENT_FAILED_WEBHOOK", "ORDER_EXPIRED_WEBHOOK":
		if err := d.Store.MarkCreditTransactionStatus(r.Context(), "cashfree", orderID, "failed"); err != nil {
			log.Printf("cashfree webhook: mark failed: %v", err)
			respond.Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})

	default:
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	}
}

// CreateCryptoInvoice opens a hosted NOWPayments checkout for a USD-denominated top-up.
func (d *Deps) CreateCryptoInvoice(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	var body struct {
		AmountUSDCents int64 `json:"amount_usd_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.AmountUSDCents < minCryptoAmountUSDCents {
		respond.Error(w, http.StatusBadRequest, "amount must be at least 100 cents")
		return
	}
	if body.AmountUSDCents > maxCryptoAmountUSDCents {
		respond.Error(w, http.StatusBadRequest, "amount exceeds maximum allowed")
		return
	}

	orderID := uuid.New().String()
	if _, err := d.Store.CreateCryptoCreditTransaction(r.Context(), userID, "nowpayments", orderID, body.AmountUSDCents); err != nil {
		log.Printf("nowpayments invoice: create ledger row: %v", err)
		respond.Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	invoice, err := d.NOWPayments.CreateInvoice(
		r.Context(),
		body.AmountUSDCents,
		orderID,
		d.BaseURL+"/payments/nowpayments/webhook",
		d.FrontendURL+"/billing?crypto=success",
		d.FrontendURL+"/billing?crypto=cancelled",
	)
	if err != nil {
		log.Printf("nowpayments invoice: %v", err)
		respond.Error(w, http.StatusBadGateway, "nowpayments invoice creation failed")
		return
	}

	respond.JSON(w, http.StatusCreated, map[string]any{
		"order_id":    orderID,
		"invoice_url": invoice.InvoiceURL,
	})
}

// NOWPaymentsWebhook is the sole completion path for crypto top-ups.
func (d *Deps) NOWPaymentsWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "could not read body")
		return
	}

	signature := r.Header.Get("x-nowpayments-sig")
	if signature == "" || !d.NOWPayments.VerifyIPNSignature(body, signature) {
		log.Printf("nowpayments webhook: rejected signature from %s", r.RemoteAddr)
		go alert.Notify(context.Background(), alert.ChannelPayments, fmt.Sprintf("rejected nowpayments webhook signature from %s", r.RemoteAddr))
		respond.Error(w, http.StatusBadRequest, "signature verification failed")
		return
	}

	var event struct {
		PaymentID     json.Number `json:"payment_id"`
		OrderID       string      `json:"order_id"`
		PaymentStatus string      `json:"payment_status"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if event.OrderID == "" {
		respond.Error(w, http.StatusBadRequest, "missing order id")
		return
	}
	paymentID := event.PaymentID.String()

	switch event.PaymentStatus {
	case "finished":
		creditedMicros, applied, err := d.Store.CompleteCreditTransaction(r.Context(), "nowpayments", event.OrderID, paymentID)
		if err != nil {
			if errors.Is(err, db.ErrCreditTransactionNotFound) {
				log.Printf("nowpayments webhook: unknown order_id %s (payment %s)", event.OrderID, paymentID)
				respond.Error(w, http.StatusBadRequest, "unknown order")
				return
			}
			log.Printf("nowpayments webhook: complete transaction: %v", err)
			go alert.Notify(context.Background(), alert.ChannelPayments, fmt.Sprintf("failed to complete crypto order %s: %v", event.OrderID, err))
			respond.Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if applied {
			go alert.Notify(context.Background(), alert.ChannelCredits, fmt.Sprintf("credited $%.2f (order %s, payment %s, via nowpayments)", float64(creditedMicros)/1e6, event.OrderID, paymentID))
		}
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})

	case "failed", "expired":
		if err := d.Store.MarkCreditTransactionStatus(r.Context(), "nowpayments", event.OrderID, "failed"); err != nil {
			log.Printf("nowpayments webhook: mark failed: %v", err)
			respond.Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})

	case "partially_paid":
		if err := d.Store.MarkCreditTransactionStatus(r.Context(), "nowpayments", event.OrderID, "partial"); err != nil {
			log.Printf("nowpayments webhook: mark partial: %v", err)
			respond.Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		go alert.Notify(context.Background(), alert.ChannelPayments, fmt.Sprintf("crypto order %s partially paid — needs manual reconciliation", event.OrderID))
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})

	default:
		respond.JSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	}
}

// PaymentProviders reports which checkout providers this deployment can
// actually take money through, and the rate needed to price the USD ones.
//
// The frontend used to hardcode availability, which meant a deployment whose
// FX lookup was failing still offered a crypto top-up that could only fail at
// invoice creation. Cashfree charges in INR and needs no rate; NOWPayments
// quotes in USD, so it is only offerable when a rate is available.
func (d *Deps) PaymentProviders(w http.ResponseWriter, r *http.Request) {
	// The top-up UI is denominated in rupees while NOWPayments settles in USD,
	// so the frontend has to convert. Serving the same rate the Cashfree path
	// pins into its ledger row keeps the two from disagreeing -- the
	// frontend's own fx.ts constant is a mock (a fixed 1/83) and would quote a
	// price that drifts from what is actually charged.
	//
	// Cached, because this is hit on every billing page mount and a live fetch
	// per request put a third-party host on the critical path of a page load.
	rate, stale, err := payments.CachedINRToUSDRate(r.Context())
	if err != nil {
		log.Printf("payment providers: fx rate: %v", err)
	} else if stale {
		log.Printf("payment providers: serving a stale fx rate (%v) — refresh is failing", rate)
	}
	usdPriceable := err == nil && rate > 0

	respond.JSON(w, http.StatusOK, map[string]any{
		"usd_per_inr": rate,
		"providers": []map[string]any{
			{"id": "cashfree", "enabled": true, "currency": "INR"},
			{"id": "nowpayments", "enabled": usdPriceable, "currency": "USD"},
		},
	})
}
