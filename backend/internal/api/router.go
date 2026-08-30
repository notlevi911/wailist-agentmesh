package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/agentmesh/backend/internal/api/handlers"
)

func NewRouter(d *handlers.Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(corsMiddleware)

	// Public routes — no JWT required
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	r.Post("/auth/signup", d.SignUp)
	r.Post("/auth/signin", d.SignIn)
	r.Post("/auth/signout", d.SignOut)
	r.Get("/auth/oauth/{provider}", d.OAuthStart)
	r.Get("/auth/oauth/{provider}/callback", d.OAuthCallback)
	r.Post("/waitlist", d.JoinWaitlist)
	r.Post("/run/{workflowId}", d.PublicTrigger)
	// Called by Cashfree's servers, not the browser — authenticated via HMAC signature
	// (x-webhook-signature), not a session cookie, so it must sit outside the JWT group.
	r.Post("/payments/cashfree/webhook", d.CashfreeWebhook)
	// Called by NOWPayments' servers, not the browser — authenticated via HMAC signature
	// (x-nowpayments-sig), not a session cookie, so it must sit outside the JWT group.
	r.Post("/payments/nowpayments/webhook", d.NOWPaymentsWebhook)
	// Called by arbitrary x402 clients (agents, other endpoints), not our own frontend —
	// no JWT session applies, so it must sit outside the JWT group.
	r.Handle("/x402/relay", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.X402Relay(w, r)
	}))
	// Static, informational resource for FundRunReserve's PaymentRequirements.Resource —
	// a real, reachable route on our own domain rather than an opaque identifier string,
	// matching what a real Bazaar-catalog crawler would expect to find there.
	r.Get("/x402/relay/run-funding", d.X402RunFundingInfo)

	// Protected routes — JWT required
	r.Group(func(r chi.Router) {
		r.Use(NewAuthMiddleware(d.JWTSecret))

		r.Get("/auth/me", d.Me)
		r.Patch("/auth/me", d.UpdateProfile)

		r.Get("/workflows", d.ListWorkflows)
		r.Post("/workflows", d.CreateWorkflow)
		r.Get("/workflows/{id}", d.GetWorkflow)
		r.Put("/workflows/{id}", d.UpdateWorkflow)
		r.Delete("/workflows/{id}", d.DeleteWorkflow)

		r.Get("/workflows/{id}/variables", d.ListVariables)
		r.Put("/workflows/{id}/variables/{key}", d.SetVariable)
		r.Delete("/workflows/{id}/variables/{key}", d.DeleteVariable)

		r.Post("/workflows/{id}/deploy", d.Deploy)
		r.Get("/workflows/{id}/agents/{agentId}/balance", d.AgentBalance)
		r.Post("/workflows/{id}/agents/{agentId}/fund", d.FundAgent)

		r.Post("/workflows/{id}/run", d.TriggerRun)
		r.Post("/workflows/{id}/stop", d.StopWorkflow)
		r.Get("/runs/{runId}", d.GetRun)
		r.Get("/runs/{runId}/stream", d.StreamRun)

		r.Post("/tools/x402/quote", d.X402Quote)

		r.Post("/payments/cashfree/order", d.CreateCashfreeOrder)
		r.Post("/payments/cashfree/verify", d.VerifyCashfreePayment)
		r.Post("/payments/nowpayments/invoice", d.CreateCryptoInvoice)
		r.Get("/credits/balance", d.GetCreditBalance)
		r.Post("/credits/redeem-coupon", d.RedeemCoupon)

		// Real spend reporting, read from debit_ledger (the rows the engine
		// writes when it actually charges) — the usage page fell back to
		// generated fixtures while these did not exist.
		r.Get("/usage/summary", d.UsageSummary)
		r.Get("/usage/timeseries", d.UsageTimeseries)
		r.Get("/usage/by-workflow", d.UsageByWorkflow)
		r.Get("/usage/by-endpoint", d.UsageByEndpoint)
		r.Get("/usage/settlements", d.UsageSettlements)
	})

	return r
}
