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
	// Same rationale, for nodes.SettlePlatformFee's PaymentRequirements.Resource.
	r.Get("/x402/relay/platform-fee", d.X402PlatformFeeInfo)
	// Same rationale, for nodes.SettleRunTotal's PaymentRequirements.Resource.
	r.Get("/x402/relay/run-total", d.X402RunTotalInfo)

	// Protected routes — JWT required
	r.Group(func(r chi.Router) {
		r.Use(NewAuthMiddleware(d.JWTSecret))
		// Pass-through unless WEB_READONLY_MODE is set; see readonly.go.
		r.Use(NewReadOnlyMiddleware())

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

		r.Put("/workflows/{id}/schedule", d.SetSchedule)
		r.Delete("/workflows/{id}/schedule", d.ClearSchedule)

		// Geofence trigger (#106). The ingest route sits inside the JWT
		// group on purpose -- unlike /run/{workflowId}, which a webhook
		// node's own per-workflow secret opens up, a geofence carries no
		// such secret, so the session is the only thing authorising it.
		r.Put("/workflows/{id}/geofence", d.SetGeofence)
		r.Delete("/workflows/{id}/geofence", d.ClearGeofence)
		r.Post("/workflows/{id}/trigger/location", d.LocationPing)

		r.Post("/workflows/{id}/deploy", d.Deploy)
		r.Post("/workflows/{id}/build", d.BuildWorkflow)
		r.Get("/workflows/{id}/agents/{agentId}/balance", d.AgentBalance)
		r.Post("/workflows/{id}/agents/{agentId}/fund", d.FundAgent)

		r.Post("/workflows/{id}/run", d.TriggerRun)
		r.Post("/workflows/{id}/stop", d.StopWorkflow)
		r.Get("/runs/{runId}", d.GetRun)
		r.Get("/runs/{runId}/stream", d.StreamRun)
		r.Post("/runs/{runId}/resume", d.ResumeRun)

		r.Post("/tools/x402/quote", d.X402Quote)
		r.Get("/bazaar/resources", d.BazaarResources)

		r.Post("/payments/cashfree/order", d.CreateCashfreeOrder)
		r.Post("/payments/cashfree/verify", d.VerifyCashfreePayment)
		r.Post("/payments/nowpayments/invoice", d.CreateCryptoInvoice)
		r.Get("/payments/providers", d.PaymentProviders)
		r.Get("/credits/balance", d.GetCreditBalance)
		r.Get("/credits/purchases", d.GetCreditPurchases)
		r.Post("/credits/redeem-coupon", d.RedeemCoupon)

		// Real spend reporting, read from debit_ledger (the rows the engine
		// writes when it actually charges) — the usage page fell back to
		// generated fixtures while these did not exist.
		r.Get("/usage/summary", d.UsageSummary)
		r.Get("/usage/timeseries", d.UsageTimeseries)
		r.Get("/usage/by-workflow", d.UsageByWorkflow)
		r.Get("/usage/by-endpoint", d.UsageByEndpoint)
		r.Get("/usage/settlements", d.UsageSettlements)

		r.Get("/tendril/machines", d.TendrilMachines)
		r.Get("/tendril/credits", d.TendrilCredits)
		r.Get("/tendril/console", d.TendrilConsoleWorkflow)
		r.Get("/tendril/console/exists", d.TendrilConsoleWorkflowExists)
		r.Post("/tendril/topup", d.TendrilConsoleTopup)
		r.Post("/tendril/rent", d.TendrilConsoleRent)
		r.Post("/tendril/run", d.TendrilConsoleRun)
		r.Get("/prism/endpoints", d.PrismEndpoints)
		r.Get("/prism/console", d.PrismConsoleWorkflow)
		r.Get("/prism/console/exists", d.PrismConsoleWorkflowExists)
		r.Post("/prism/run", d.PrismConsoleRun)
		r.Get("/leases", d.ListLeases)
		r.Post("/leases/{id}/release", d.ReleaseLease)
		r.Get("/leases/{id}/key", d.DownloadLeaseKey)
		r.Get("/leases/{id}/terminal", d.LeaseTerminal)

		// Connects an external account (Gmail/Sheets/Calendar/Drive today) a
		// workflow node calls on this user's behalf -- distinct from
		// /auth/oauth above, which signs a person INTO AgentMesh and lives
		// outside this group since there's no session yet at that point.
		// This flow requires one already, hence living here instead.
		r.Get("/oauth2/{provider}/start", d.OAuth2CredStart)
		r.Get("/oauth2/{provider}/callback", d.OAuth2CredCallback)
		r.Get("/oauth2/credentials", d.OAuth2CredList)
		r.Delete("/oauth2/credentials/{id}", d.OAuth2CredDelete)

		// Connector account-linking (Slack/GitHub/Notion/etc, #42) -- a
		// separate OAuth surface from oauth2/* above: that one is Google's
		// four products sharing one consent screen, this one is one
		// provider per connector node, each with its own authorize/token
		// endpoint registered in connector_oauth.go's provider registry.
		r.Get("/connectors/oauth/{provider}/start", d.ConnectorOAuthStart)
		r.Get("/connectors/oauth/{provider}/callback", d.ConnectorOAuthCallback)
	})

	return r
}
