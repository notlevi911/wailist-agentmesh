# Contributing to AgentMesh

This document is for developers who want to contribute to the AgentMesh platform itself. If you want to build a paid x402 API endpoint that agents can use, see [`x402/CONTRIBUTING.md`](./x402/CONTRIBUTING.md) instead.

---

## Table of contents

- [How the application works](#how-the-application-works)
- [Project structure](#project-structure)
- [Running locally](#running-locally)
- [Environment variables](#environment-variables)
- [API reference](#api-reference)
- [Key concepts](#key-concepts)
- [Frontend architecture](#frontend-architecture)
- [Backend architecture](#backend-architecture)
- [Database schema](#database-schema)
- [Hacktoberfest](#hacktoberfest)
- [Making a contribution](#making-a-contribution)

---

## How the application works

AgentMesh lets users build autonomous AI agent workflows visually. Here's the full lifecycle:

**1. Build** — The user opens the canvas, drags out nodes (Trigger, Agent, Tool, Action), and wires them together. The canvas is a custom SVG renderer — no third-party canvas library.

**2. Configure** — Each node has a config panel. Agent nodes take an LLM provider + model + system prompt. x402 Tool nodes take an endpoint URL — clicking "Discover" sends a `POST /tools/x402/quote` to the backend, which hits the endpoint, gets the `402` response, and returns the price and parameter schema.

**3. Deploy** — `POST /workflows/:id/deploy` walks every Agent node in the workflow graph and provisions a real Algorand Ed25519 keypair for each one. The mnemonic is encrypted with AES-GCM and stored in the `agent_wallets` table. The public address is stored in the workflow graph.

**4. Fund** — `POST /workflows/:id/agents/:agentId/fund` calls the Algorand testnet dispenser to drop ALGO into the agent's wallet.

**5. Run** — `POST /workflows/:id/run` starts a workflow run. The backend:
- Performs a topological sort of the workflow graph
- Executes nodes level by level (nodes in the same level run in parallel goroutines)
- For Agent nodes, runs a full LLM function-calling loop (up to 15 iterations)
- For x402 Tool nodes attached to an agent, skips standalone execution — the agent's LLM decides when and how to call them via function calling
- Streams every log line in real time over SSE (`GET /runs/:runId/stream`)

**6. Payment** — When the agent's LLM decides to call an x402 tool, the runner calls `ExecuteTool402`:
- Hits the endpoint → receives `402` with price + recipient
- Decrypts the agent's mnemonic → signs an Algorand payment transaction
- Submits to the network → retries the request with `X-Payment-Txid` header
- Returns the result back to the LLM as a function response

The LLM continues reasoning with the result and may call more tools, up to 15 iterations.

---

## Project structure

```
agentmesh/
├── frontend/               Next.js 16 app (React 19, Tailwind 4, TypeScript)
│   └── src/
│       ├── app/            App Router pages
│       ├── components/
│       │   └── canvas/     Custom SVG canvas — all node rendering and wiring
│       ├── hooks/          useAuth, useWorkflow, etc.
│       └── lib/
│           ├── api.ts      All fetch calls to the backend
│           ├── types.ts    WorkflowNode, WorkflowEdge, NodeType, etc.
│           ├── portUtils.ts  Port coordinate math and connection validation
│           └── data.ts     Node templates and sample workflows
│
├── backend/
│   ├── cmd/server/         Entry point — sets up router, runs migrations, starts HTTP
│   ├── cmd/walletgen/      Dev tool — generate an Algorand keypair
│   ├── cmd/x402pay/        Dev tool — drive an x402 payment from the CLI
│   └── internal/
│       ├── api/
│       │   ├── router.go   Route table
│       │   ├── handlers/   One file per resource (auth, oauth, connector_oauth,
│       │   │               oauth2creds, workflows, runs, deploy, tools, bazaar,
│       │   │               payments, usage, secrets, leases, tendril_console,
│       │   │               x402relay, waitlist)
│       │   └── middleware.go  CORS, JWT auth, SSE token fallback
│       ├── engine/
│       │   ├── runner.go   Topological executor — parallel level execution
│       │   ├── graph.go    Topological sort + attach-map
│       │   ├── context.go  RunContext + per-run debit ledger
│       │   ├── tendril_reaper.go  Releases leases whose funded window closed
│       │   └── nodes/
│       │       ├── provider.go       LLM callers + agentic function-calling loop
│       │       ├── tool402.go / walletpay.go / runfund.go  x402 payment + run-level funding
│       │       ├── tool.go           HTTP tool + SSRF protection + websearch/calc/datetime
│       │       ├── action.go + connectors_*.go + google.go  email / webhook / ~40 connectors
│       │       ├── bazaar.go         Bazaar discovery extension
│       │       ├── billing.go / tier.go  BYOK flat-fee billing
│       │       └── tendril.go        Compute leasing
│       ├── db/             PostgreSQL queries (pgx/v5) + migrations
│       ├── models/         Shared types — WorkflowNode, ParamDef, AgentWallet, etc.
│       ├── bazaar/         Mirrors GoPlausible's x402 catalog, filtered to payable Algorand endpoints
│       ├── payments/       Cashfree (INR) + NOWPayments (crypto) clients
│       ├── oauthcred/      Persisted, refreshable OAuth2 connections for workflow nodes
│       ├── tendril/        Typed client for the Tendril compute registry
│       ├── sshkeys/        Per-lease ed25519 keypair for a rented machine
│       ├── alert/          Optional Discord-webhook audit log
│       ├── x402/           Schema-exact v2 payment payload types
│       ├── sse/            In-process pub/sub for streaming run logs
│       └── wallet/         Algorand keypair generation, encryption, signing
│
└── x402/                   Protocol spec and example server submodule
```

---

## Running locally

### Prerequisites

- Node.js 18+
- Docker (for the bundled Postgres + backend compose setup — recommended)
- Go 1.22+ (only needed if you'd rather run the backend natively)

### Backend

**Fastest path — Docker Compose.** Spins up Postgres and the backend together, no `.env` file or external Supabase connection needed. This is the recommended setup if you're only working on the frontend and just need a working backend to point at.

```bash
docker compose up -d
# → postgres on :5432, backend on :8080
curl http://localhost:8080/health   # → ok
```

Migrations run automatically on first boot. Uses fixed dev-only secrets baked into `docker-compose.yml` — never reuse those values anywhere real. To reset the database: `docker compose down -v`.

**Native alternative** — if you're actively developing backend code and want faster rebuild cycles:

```bash
cd backend
cp .env.example .env      # fill in the required values (see below)
go run ./cmd/server
# → listening on :8080
```

Requires PostgreSQL 14+ (or `docker run -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16`). Migrations run automatically on startup either way.

### Frontend

```bash
cd frontend
echo "NEXT_PUBLIC_API_URL=http://localhost:8080" > .env.local
npm install
npm run dev
# → localhost:3000
```

---

## Environment variables

All backend config lives in `backend/.env`. Required vars:

| Variable | Description |
|---|---|
| `DATABASE_URL` | Postgres connection string |
| `JWT_SECRET` | Min 32 bytes — `openssl rand -hex 32` |
| `ENCRYPTION_KEY` | 32-byte key for AES-GCM mnemonic encryption |
| `BASE_URL` | Public URL of the backend (used to build OAuth callback URLs) |
| `FRONTEND_URL` | Public URL of the frontend (OAuth redirects) |
| `CORS_ORIGIN` | Allowed browser origin — must match frontend URL exactly, no trailing slash |

Optional (needed for specific features):

| Variable | Feature |
|---|---|
| `GEMINI_API_KEY` | Gemini LLM nodes |
| `OPENAI_API_KEY` | OpenAI LLM nodes |
| `ANTHROPIC_API_KEY` | Anthropic LLM nodes |
| `GROQ_API_KEY` | Groq LLM nodes |
| `MISTRAL_API_KEY` | Mistral LLM nodes |
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | GitHub OAuth |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | Google OAuth |
| `RESEND_API_KEY` | Email action node |
| `TENDRIL_REGISTRY_URL` | Tendril compute registry (default `https://tendrilregister.007575.xyz`). Unset disables tendril nodes — they fail closed. |
| `MAX_RELAY_OUTBOUND_USD_MICROS` | Ceiling on one relayed x402 payment (default `20000000`, $20.00 — raised from $5.00 because a 2-hour rent on a $6/hr machine tops the pool up by $12 in a single call). |

### The two Tendril balances

Tendril keys credit to the paying address, and the payer is always Wallet 2 (`PLATFORM_WALLET_*`) — so on Tendril's own side there is exactly **one** balance for all of AgentMesh combined. AgentMesh therefore keeps a **per-user sub-ledger** on top of that shared pool: `users.tendril_credit_usd_micros` plus the append-only `tendril_credit_ledger` table. A user may only ever spend what they themselves converted into it.

A Topup node settles real USDC into the shared pool and, in the same transaction, moves the same value from that user's AgentMesh credits into their Tendril credits — one transfer, two ledger rows. Renting is a flat 1¢ gate fee that does **not** buy time; the hours themselves come out of the user's Tendril credit at the machine's hourly rate. Release is where compute is actually billed — Tendril reports what it charged, and the unused remainder of the reservation returns to that user's Tendril credit (never to AgentMesh credits, which would let a user cycle rent/release to convert Tendril credit back into general platform credit the pool cannot honor). The reaper exists because an unreleased lease keeps metering against the pool even after its funded window closes.

The invariant every Tendril-touching change must preserve:

```
SUM(users.tendril_credit_usd_micros) + (hours currently metering) <= Tendril pool balance at Wallet 2
```

---

## API reference

### Public endpoints (no auth)

| Method | Route | Description |
|---|---|---|
| `GET` | `/health` | Returns `ok` |
| `POST` | `/auth/signup` | Create account — body: `{ email, password }` → sets auth cookie |
| `POST` | `/auth/signin` | Sign in — body: `{ email, password }` → sets auth cookie |
| `POST` | `/auth/signout` | Clears auth cookie |
| `GET` | `/auth/oauth/:provider` | Start OAuth (`github` or `google`) |
| `GET` | `/auth/oauth/:provider/callback` | OAuth callback — sets cookie, redirects to frontend |
| `POST` | `/waitlist` | Join waitlist — body: `{ email }` |
| `POST` | `/run/:workflowId` | Public webhook trigger (only works on deployed workflows with a trigger node) |

### Protected endpoints (require auth cookie or `Authorization: Bearer <token>`)

| Method | Route | Description |
|---|---|---|
| `GET` | `/auth/me` | Returns `{ id }` for the current user |
| `GET` | `/workflows` | List the user's workflows |
| `POST` | `/workflows` | Create a workflow — body: `{ name, graph }` |
| `GET` | `/workflows/:id` | Get a workflow (API keys masked in response) |
| `PUT` | `/workflows/:id` | Update a workflow |
| `DELETE` | `/workflows/:id` | Delete a workflow |
| `POST` | `/workflows/:id/deploy` | Provision Algorand wallets for all agent nodes |
| `GET` | `/workflows/:id/agents/:agentId/balance` | Get agent wallet ALGO balance |
| `POST` | `/workflows/:id/agents/:agentId/fund` | Fund agent from testnet dispenser |
| `POST` | `/workflows/:id/run` | Start a workflow run — body: `{ input }` |
| `POST` | `/workflows/:id/stop` | Stop a running workflow |
| `GET` | `/runs/:runId` | Get a run and its logs |
| `GET` | `/runs/:runId/stream` | SSE stream of live run logs — supports `?token=<jwt>` for EventSource |
| `POST` | `/tools/x402/quote` | Discover price + params for an x402 endpoint — body: `{ url }` |

### Auth

JWT is delivered as an `HttpOnly` cookie (`agentmesh_token`) on signin/signup. The cookie is `SameSite=None; Secure` in production (cross-site frontend/backend on different domains) and `SameSite=Lax` in local dev.

For SSE (`GET /runs/:runId/stream`), EventSource can't set headers, so the JWT can also be passed as `?token=<jwt>` query param — the auth middleware checks this as a fallback.

---

## Key concepts

### Workflow graph

A workflow is a JSON graph of `WorkflowNode` objects and `WorkflowEdge` objects. Nodes have a `type` (`trigger`, `agent`, `tool402`, `action`), a `config` object, and `inputs`/`outputs` port definitions. Edges connect output ports to input ports.

The full type is in `backend/internal/models/types.go` and mirrored in `frontend/src/lib/types.ts`.

### agentToolIDs

When a run starts, the runner builds `agentToolIDs` — the set of tool node IDs that are connected to an agent exclusively via `attach → tools` edges. These nodes are **skipped** from the topological execution. The agent's LLM drives them via function calling instead. This is what makes the agentic loop work — the LLM decides when to call a tool, not the graph executor.

### DiscoveredParams

When a user clicks "Discover" on an x402 Tool node, the backend hits the endpoint, reads the `params` array from the `402` response, and stores it on the node as `DiscoveredParams`. At run time, these become `functionDeclarations` passed to the LLM, so the model knows what parameters the tool accepts.

### LLM function-calling loop

All five providers — Gemini, OpenAI, Anthropic, Groq, Mistral — run the loop (Groq and Mistral over the OpenAI-compatible path):

1. Call LLM with system prompt, user input, and function declarations built from `DiscoveredParams`
2. If the LLM returns a `functionCall` → execute the tool, feed the result back as a function response
3. Call LLM again with the updated conversation
4. Repeat up to `maxToolIterations` (15) until the LLM returns a plain text response

Keys are BYOK by default; a platform-key mode falls back to platform-held keys with metered, cost-plus usage.

### SSE streaming

Each run has a channel in the in-process SSE broker (`internal/sse`). The runner publishes a log line after every node completes. `GET /runs/:runId/stream` subscribes and streams `event: log` lines to the browser. A 30-second keepalive ping prevents proxies from closing idle connections.

---

## Frontend architecture

**App Router** pages:

| Route | Auth | Description |
|---|---|---|
| `/` | public | Landing page |
| `/signin`, `/signup` | public | Auth forms |
| `/auth/callback` | public | OAuth token landing |
| `/workflows` | protected | Workflow list |
| `/workflows/:id` | protected | Canvas editor |

**Canvas** (`src/components/canvas/`): All rendering is custom SVG — no ReactFlow or similar. `CanvasPage.tsx` owns all state. `CanvasGraph.tsx` handles the SVG surface, drag/drop, and port-to-port wiring. Node components live in `canvas/nodes/index.tsx`.

**Auth** (`src/hooks/useAuth.ts`): Reads `agentmesh_token` from `localStorage`. The token is a real HS256 JWT issued by the backend.

**API layer** (`src/lib/api.ts`): All calls go to `NEXT_PUBLIC_API_URL`. This is inlined at build time — set it in `.env.local` and restart the dev server.

---

## Backend architecture

**Router** (`internal/api/`): chi v5. Middleware stack: CORS → auth (on protected routes) → handler.

**Engine** (`internal/engine/runner.go`): Topological sort → parallel level execution. Each node runs in its own goroutine within a level. Results pass between nodes via the `RunContext`.

**Node executors** (`internal/engine/nodes/`):
- `provider.go` — LLM function-calling loop for Gemini, OpenAI, Anthropic, Groq, Mistral
- `tool402.go` — full x402 payment flow (fetch → 402 → sign → pay → retry)
- `tool.go` — standard HTTP tool with SSRF protection (DNS-resolved, private IP blocked)
- `action.go` — email via Resend API, webhook POST

**Wallet** (`internal/wallet/`): Algorand Ed25519 keypair generation, AES-GCM mnemonic encryption, transaction signing via the Algorand Go SDK.

**DB** (`internal/db/`): pgx/v5, golang-migrate for schema versioning. Migrations run automatically on server start. The Supabase transaction pooler runs PgBouncer in transaction mode — the backend forces simple query protocol to stay compatible.

---

## Database schema

| Table | Description |
|---|---|
| `users` | `id`, `email` (unique), `password_hash` (empty for OAuth users), `created_at` |
| `workflows` | `id`, `user_id`, `name`, `graph` (JSONB), `status`, `deploy_metadata`, `created_at` |
| `agent_wallets` | `id`, `workflow_id`, `agent_node_id`, `address`, `encrypted_mnemonic` |
| `tool_credentials` | Encrypted per-provider API keys scoped to a workflow |
| `runs` | `id`, `workflow_id`, `status`, `input`, `output`, `created_at` |
| `run_logs` | `id`, `run_id`, `node_id`, `level`, `message`, `created_at` |
| `waitlist` | `id`, `email` (unique), `created_at` |

---

## Tests

### Running the test suite

```bash
cd backend
go test -p 1 ./...           # run all tests
go test -p 1 ./... -cover    # with coverage percentages
go test -p 1 ./... -v        # verbose — see each test name pass/fail
go test ./internal/engine/nodes/... -run TestX402  # run a specific test or pattern
```

`-p 1` matters whenever `TEST_DATABASE_URL` is set (see below): every DB-touching package independently runs golang-migrate's `Up()` against the same database at test-setup time, and Go's default package parallelism can start several of those at once, occasionally deadlocking on `pg_advisory_lock` (`SQLSTATE 40P01`). Running a single package (like the `-run TestX402` example above) never hits this, so it's fine without `-p 1`. CI only applies `-p 1` to the packages that actually touch the database (`internal/api/handlers`, `internal/db`, `internal/engine`, `internal/scheduler`) so everything else still runs in parallel — `-p 1 ./...` here is just the simpler one-command version for local runs.

### Current coverage by package

| Package | Coverage | Notes |
|---|---|---|
| `internal/api` | 89% | Middleware, CORS, JWT, health check |
| `internal/api/handlers` | 11% | Unit tests pass; most handler tests require a real DB and are skipped without `TEST_DATABASE_URL` |
| `internal/engine` | 27% | Graph/topological sort fully covered; runner integration tests need DB |
| `internal/engine/nodes` | — | Build error in `provider_test.go` (see below) |
| `internal/sse` | 83% | Broker publish/subscribe fully covered |
| `internal/wallet` | 37% | Keypair generation and AES-GCM encrypt/decrypt covered |
| `internal/db` | 0% | All DB tests are integration tests — skipped without `TEST_DATABASE_URL` |
| `internal/models` | — | No statements (types only) |

### What each package tests

**`internal/api`**
- `TestNewAuthMiddlewareRejectsNoToken` — middleware returns 401 with no token
- `TestNewAuthMiddlewareAcceptsValidToken` — valid HS256 JWT passes through
- `TestHealthCheck` — `GET /health` returns 200

**`internal/api/handlers`**
- `TestSignUpReturnsBadRequestOnEmptyEmail` — 400 on missing email
- `TestSignUpReturnsBadRequestOnShortPassword` — 400 on < 8 char password
- `TestEncryptField_*` / `TestMaskNodes_*` / `TestDecryptNodes_*` — API key encryption/masking round-trips
- `TestDeploy`, `TestTriggerRun`, `TestCreateAndGetWorkflow`, `TestAPIKeyEncryption`, `TestStopWorkflow*` — skipped without `TEST_DATABASE_URL`

**`internal/engine`**
- `TestTopologicalSort` — graph with no cycles sorts correctly
- `TestCycleDetected` — graph with a cycle returns an error
- `TestBuildAttachMap` — agent→tool attach edges parsed correctly
- `TestStopReturns*` — runner stop signal tests (skipped without DB)

**`internal/engine/nodes`**
- `TestX402FreeEndpoint` — endpoint that returns 200 directly (no payment needed)
- `TestX402ParseQuote` — endpoint returns 402, payment descriptor is parsed correctly
- `TestX402PaymentSigned` — full flow: 402 → signer called → retry with txid → success
- `TestX402NoWallet` — 402 response with no wallet configured returns a graceful error
- `TestX402SignerError` — signer failure surfaces as an error
- `TestWebhookAction` — action node POSTs to a webhook URL
- `TestLogAction` — log action writes to RunContext
- `TestCalculator` — `calc` tool evaluates math expressions
- `TestDatetime` — `datetime` tool returns current time
- `TestHTTPTool` — standard HTTP GET tool with mock server
- ⚠️ `provider_test.go` has a build error — `ExecuteAgent` signature changed, test not updated yet (good first issue)

**`internal/sse`**
- `TestBrokerPublishSubscribe` — messages published to a channel are received by all subscribers

**`internal/wallet`**
- `TestGenerateWallet` — generates a valid Algorand Ed25519 keypair with a correct-length address
- `TestEncryptDecrypt` — AES-GCM mnemonic encrypt → decrypt round-trip

### Integration tests (require a real database)

Tests in `internal/db` and several handler/runner tests are skipped unless `TEST_DATABASE_URL` is set:

```bash
TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/agentmesh_test go test -p 1 ./...
```

These tests create and tear down their own schema, so they're safe to run against a throwaway local database. Do **not** point them at a production database.

### Writing new tests

- Unit tests live alongside the code they test (`foo.go` → `foo_test.go`) in the same package
- Use `httptest.NewRecorder()` and `httptest.NewServer()` for HTTP handler/client tests — see `tool_test.go` for examples
- For anything that touches the DB, guard with a skip:
  ```go
  db := os.Getenv("TEST_DATABASE_URL")
  if db == "" {
      t.Skip("TEST_DATABASE_URL not set")
  }
  ```
- Don't mock the database in unit tests that are meant to test DB logic — use a real test DB or skip

---

## Hacktoberfest

This repo participates in **Hacktoberfest**. Pull requests opened against it during October count toward your Hacktoberfest total once a maintainer merges them, approves them, or labels them `hacktoberfest-accepted`.

**What counts**

- A PR that fixes a real bug, adds a genuinely useful feature, or improves docs/tests in a way we'd merge any month of the year.
- Work tied to an issue labelled `hacktoberfest` or `good first issue`. Comment on the issue to claim it before you start so two people don't collide.

**What gets your PR marked `spam` / `invalid` (and does not count)**

- Whitespace-only diffs, README typo swaps with no substance, automated or templated PRs, duplicate PRs across many repos.
- Anything that ignores the contribution rules below — untested changes, unrelated file churn, drive-by formatter runs.

**Good starting points**

- `provider_test.go` has a known build error — `ExecuteAgent`'s signature changed and the test wasn't updated. Small, self-contained, well-scoped.
- Filter issues by the `good first issue` and `hacktoberfest` labels.

Two low-effort PRs flagged as spam earn a Hacktoberfest disqualification for the whole event, so aim for one solid PR over several throwaway ones. If you're unsure whether an idea is substantial enough, open an issue and ask first.

---

## Making a contribution

1. **Open an issue first** for anything non-trivial — feature ideas, architectural changes, new node types. No point writing code that won't get merged.
2. **Fork and branch** — name your branch something descriptive (`feat/memory-node`, `fix/sse-timeout`).
3. **Keep PRs focused** — one thing per PR. A bug fix doesn't need a refactor alongside it.
4. **Match the existing style** — no formatter changes, no linting rule updates, no unrelated file edits.
5. **Test what you touch** — add or update `_test.go` files for whatever you change. Run `go test -p 1 ./...` before opening a PR.
6. **Open the PR against `master`**.

For bugs: include what you expected, what happened, and how to reproduce it.
For features: include why it matters and roughly how you plan to build it.
