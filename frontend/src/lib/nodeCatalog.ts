// The node catalog: every node type, template and field the canvas really
// offers, in one plain object.
//
// It exists for the chat workflow builder. The builder used to work from a
// hand-typed list in its own prompt, which drifted: it offered a "cron"
// trigger that has never existed, knew 24 of the 42 connectors, could not
// create state or Google nodes at all, and had no idea which settings a node
// has -- so it built a JSON Extract step with no path and reported that it
// "extracts lastPrice". Everything here is assembled from what the canvas
// itself uses (the palette in data.ts, the connector tables in
// connectorFields.ts, the presets PalettePanel applies on drop), plus
// hand-written specs for the node types whose Inspector is bespoke JSX.
//
// The backend cannot import TypeScript, so this is serialised to
// backend/internal/engine/nodes/nodecatalog.json and embedded there.
// nodeCatalog.sync.test.ts fails when that file is stale; regenerate it with
// `npm run gen:node-catalog`. A Go test on the other side checks every
// template and key here against what the engine actually executes.

import {
  TRIGGER_TEMPLATES,
  AGENT_TEMPLATES,
  PROVIDER_TEMPLATES,
  TOOL_TEMPLATES,
  ACTION_TEMPLATES,
  GOOGLE_TEMPLATES,
  STATE_TEMPLATES,
  TENDRIL_TEMPLATES,
  END_TEMPLATES,
} from "./data";
import {
  CONNECTOR_CONFIG_FIELDS,
  CONNECTOR_AUTH,
  type ConnectorField,
} from "./connectorFields";

/**
 * Where a field's value lives on the saved node, which is also what decides
 * whether the builder may set it:
 * - field:      a top-level WorkflowNode property (url, stateKey, …)
 * - config:     node.config[key] — non-secret settings
 * - secret:     node.secrets[key] or an encrypted top-level key — a credential
 *               the builder never sees and must never invent; it names it for
 *               the user instead
 * - connection: an account the user links in the Inspector (e.g. Google OAuth)
 */
export type CatalogFieldWhere = "field" | "config" | "secret" | "connection";

export interface CatalogField {
  key: string;
  where: CatalogFieldWhere;
  label: string;
  hint?: string;
  placeholder?: string;
}

export interface CatalogTemplate {
  id: string;
  name: string;
  desc: string;
  /**
   * What kind of work the template does, which is what decides whether its
   * failure may be degraded into an error payload the run continues with:
   * - "read":    fetches from a source outside the workflow, which can be
   *              down. Its failure may be degraded.
   * - "compute": pure local computation, or a read of the workflow's own
   *              saved state. Nothing outside can make it fail, so a failure
   *              is a fault in the workflow and must fail the run.
   * - "action":  sends, pays or writes. Its failure fails the run.
   */
  kind: "read" | "compute" | "action";
  /** A caveat the builder must respect, e.g. behaviour that is not implemented. */
  note?: string;
  /** Fields set automatically when the node is created, exactly as the palette does. */
  presets?: Record<string, string>;
  fields: CatalogField[];
  /** Where the user obtains this node's credential. */
  authDocUrl?: string;
  /** The connector can alternatively be linked with this OAuth provider. */
  oauthProvider?: string;
}

export interface CatalogType {
  type: string;
  desc: string;
  templates: CatalogTemplate[];
}

export interface NodeCatalog {
  version: 1;
  types: CatalogType[];
}

const MESSAGE_TEMPLATE: CatalogField = {
  key: "messageTemplate",
  where: "config",
  label: "Message",
  hint: "what this step sends; blank sends the previous step's output. {{ result }} / {{ result.field }} / {{ node.<id> }} / {{ state.key }} interpolate",
};

function fromConnectorTable(id: string): CatalogField[] {
  const spec = CONNECTOR_CONFIG_FIELDS[id];
  if (!spec) return [];
  return spec.fields.map((f: ConnectorField) => ({
    key: f.key,
    where: f.kind,
    label: f.label,
    ...(f.hint ? { hint: f.catalogHint ?? f.hint } : {}),
    ...(f.placeholder ? { placeholder: f.catalogPlaceholder ?? f.placeholder } : {}),
  }));
}

// Tool templates whose Inspector is not table-driven. The compute tools
// (set, json_extract, crypto, …) are table-driven and come from
// connectorFields.ts like the connectors do.
const TOOL_FIELDS: Record<string, CatalogField[]> = {
  http: [
    { key: "url", where: "field", label: "URL", placeholder: "https://api.example.com/v1/" },
    { key: "method", where: "field", label: "Method", placeholder: "GET" },
    {
      key: "httpBodyTemplate",
      where: "config",
      label: "Body template",
      hint: "only sent on POST/PUT/PATCH/DELETE -- {{ result }} or {{ result.field }}",
    },
    {
      key: "httpHeadersJSON",
      where: "secret",
      label: "Headers",
      hint: 'JSON object of header -> value, e.g. {"X-Api-Key": "…"}',
    },
    { key: "httpBasicUser", where: "secret", label: "Basic auth username" },
    { key: "httpBasicPass", where: "secret", label: "Basic auth password" },
  ],
  calc: [
    {
      key: "url",
      where: "field",
      label: "Expression",
      hint: "a math expression such as (2+3)*4 -- the calculator stores it in the url field",
    },
  ],
  websearch: [
    {
      key: "searchQuery",
      where: "config",
      label: "Search query",
      hint: "what to search for when this node runs in the flow; {{ result }} inserts the previous step's output",
    },
  ],
  xml: [],
};

const TOOL_NOTES: Record<string, string> = {
  websearch:
    "Answers with a live Google Search. Attached to an agent's tools port it searches whatever the agent asks and needs no settings. As a FLOW step it needs searchQuery, or it searches the previous step's output -- and a manual trigger produces no output, so a flow node fed by one with no searchQuery fails at run time.",
  xml: "Parses the previous step's XML output into JSON. No settings.",
};

const GOOGLE_ACCOUNT: CatalogField = {
  key: "oauthCredentialID",
  where: "connection",
  label: "Google account",
  hint: "the user links a Google account in the Inspector",
};

const GOOGLE_FIELDS: Record<string, CatalogField[]> = {
  gmail_send: [
    { key: "gmailTo", where: "config", label: "To", placeholder: "recipient@example.com" },
    { key: "gmailSubject", where: "config", label: "Subject" },
  ],
  gmail_reply: [
    { key: "gmailTo", where: "config", label: "To", placeholder: "recipient@example.com" },
    { key: "gmailSubject", where: "config", label: "Subject" },
    { key: "gmailThreadID", where: "config", label: "Thread ID", hint: "keeps the reply in the original thread, e.g. {{ result.threadId }}" },
  ],
  sheets_read: [
    { key: "sheetsSpreadsheetID", where: "config", label: "Spreadsheet ID", hint: "the long id in the sheet's URL" },
    { key: "sheetsRange", where: "config", label: "Range", placeholder: "Sheet1!A1:Z1000" },
  ],
  sheets_append: [
    { key: "sheetsSpreadsheetID", where: "config", label: "Spreadsheet ID", hint: "the long id in the sheet's URL" },
    { key: "sheetsRange", where: "config", label: "Range", placeholder: "Sheet1!A1:Z1000" },
  ],
  calendar_list: [
    { key: "calendarID", where: "config", label: "Calendar ID", hint: "leave blank for the primary calendar", placeholder: "primary" },
  ],
  calendar_create: [
    { key: "calendarID", where: "config", label: "Calendar ID", hint: "leave blank for the primary calendar", placeholder: "primary" },
    { key: "calendarSummary", where: "config", label: "Title", hint: "leave blank to use the Message field" },
    { key: "calendarStart", where: "config", label: "Start", placeholder: "2026-08-10T10:00:00Z" },
    { key: "calendarEnd", where: "config", label: "End", placeholder: "2026-08-10T11:00:00Z" },
  ],
};

const EMAIL_FIELDS: CatalogField[] = [
  { key: "emailProvider", where: "field", label: "Provider", hint: "resend, postmark, sendgrid or brevo", placeholder: "resend" },
  { key: "emailFrom", where: "field", label: "From", hint: "must be verified with the provider", placeholder: "AgentMesh <you@yourdomain.com>" },
  { key: "emailTo", where: "field", label: "To", hint: "{{ variables }} supported", placeholder: "recipient@example.com" },
  { key: "emailSubject", where: "field", label: "Subject" },
  { key: "emailBody", where: "field", label: "Body", hint: "blank sends the previous step's output; {{ variables }} supported" },
  { key: "emailApiKey", where: "secret", label: "API key", hint: "the chosen email provider's API key" },
];

// Action notes, keyed by template.
const ACTION_NOTES: Record<string, string> = {
  algorand_account:
    "Current state only: balance and ASA holdings for one address. It cannot answer transaction history: use algorand_transactions for that, never a paid x402 endpoint. algo is an exact decimal string in whole ALGO (quote it as-is) and algoMicro is the raw integer in microalgos; minBalance and minBalanceMicro are the same pair. Each holding's amount is already decimal-adjusted (a string such as \"1.5\" with its unitName): quote it as-is. amountBaseUnits is the raw integer in base units and must never be reported as a token amount. A holding with no amount could not be looked up; unresolvedAssets counts those. To describe an asset this account does NOT hold, use algorand_asset.",
  algorand_transactions:
    "Recent transaction history for one address, newest first, read from an Algorand indexer. This is the node for \"what did this address do\"; algorand_account cannot answer it. Each row carries type (pay, axfer, appl, ...), direction (out if this address sent it, in if it received it, other otherwise), sender, receiver, round, time as an RFC3339 string, and an amount. A pay row has algo (an exact decimal string in whole ALGO, quote it as-is) and algoMicro (the raw integer). An axfer row has amount (already decimal-adjusted, quote it as-is), amountBaseUnits (the raw integer, never report this as a token amount), assetId and unitName. A row with no amount could not have its asset looked up; unresolvedAssets counts those. Set algoTxLimit for how many (10 by default, 50 at most). algoTxType narrows to one type and must be the chain's own code -- pay, axfer, appl, acfg, afrz, keyreg, stpf or hb (a payment is \"pay\", not \"payment\") -- or blank for every type; anything else fails the step. The output's type field says which filter was applied.",
  algorand_asset:
    "Describes one ASA by id: name, unitName, decimals, total supply, creator and the manager/reserve/freeze/clawback addresses. algoAssetId is the asset's NUMBER, never its ticker -- 31566704 is USDC. Use this for \"what is this token\" about an asset nobody in the workflow holds; algorand_account only describes assets the account being read already holds. total is an exact decimal string and totalBaseUnits is the raw integer.",
  // A live build turned the user's "myrad" into "myriad" and then "myria" --
  // two other coins -- by web-searching for the id. CoinGecko's own search
  // resolves "myrad" to exactly one coin.
  coingecko:
    "cgIDs are CoinGecko coin ids, not names or symbols. Get each one from resolve_coin, which is the only source add_node accepts -- an id from fetch_url or web_search is refused. Use the id of the match whose name or symbol matches what the user said, never a similar-sounding coin. If resolve_coin finds nothing, that token is not listed: say so and stop. When CoinGecko is unavailable, well-known coins are priced from Coinbase or CoinPaprika instead, in the same shape plus source and note fields, and any coin that could not be priced is listed in unavailable: an agent reading this should name the source when source is present and say which coins are unavailable.",
  coingecko_history:
    "One coin only. cgID is a CoinGecko coin id and MUST come from resolve_coin -- a name or symbol will 404. Returns first, last, high, low and changePct already computed, plus the points; quote those fields rather than working them out from the points yourself. When CoinGecko is unavailable the history may come from Coinbase closing prices instead; source and note then say so.",
};

const TRIGGER_NOTES: Record<string, string> = {
  manual:
    "Started by the Run button -- and by the workflow's schedule, if one is set. Use this for anything that should run on a timetable.",
  chat: "Started by a message typed into the chat console; that message is the run's input.",
  webhook:
    "Started by an HTTP POST to the workflow's webhook URL, shown in the Inspector once deployed. The signing secret is generated automatically.",
};

const AGENT_NOTES: Record<string, string> = {
  router:
    "Runs exactly like a plain AI agent today -- the engine has no routing step. Do not promise the user classification-based dispatch.",
  human:
    "Runs exactly like a plain AI agent today -- the engine has no approval gate and never pauses for a human. Do not promise the user an approval step.",
};

/**
 * How every template is classified, keyed "<type>/<id>" because ids repeat
 * across types ("http" is both a tool and an end node).
 *
 * tool/http is deliberately in neither set: it is classified at run time by
 * its method, since the same template GETs or POSTs depending on its config.
 */
// Templates that are pure local computation, or that read only what this
// workflow itself saved. Nothing outside the workflow can make one of these
// fail, so a failure is a fault in the workflow -- a malformed expression, a
// jsonPath that does not match -- and it fails the run rather than degrading.
// Degrading them would turn an authoring bug into an answer that says "the
// step failed" on this run and on every run after it, since the same input
// produces the same failure forever.
//
// Every one of these executes entirely inside the process: none of the tool
// cases in the engine's tool.go makes a network call, and state/get reads a
// value this workflow stored.
const COMPUTE_TEMPLATES = new Set<string>([
  "tool/calc",
  "tool/set",
  "tool/json_extract",
  "tool/crypto",
  "tool/datetime",
  "tool/xml",
  "tool/template",
  "tool/html_extract",
  "tool/markdown",
  "tool/quickchart",
  // Reading a variable is safe, but it is this workflow's own value, not a
  // live source -- so a miss is a wrong key, not an outage.
  "state/get",
]);

// Templates that fetch from a source outside the workflow. These are the only
// ones whose failure may be degraded: the source really can be unavailable,
// and on the next run it may well be back.
const READ_TEMPLATES = new Set<string>([
  // The only tool that leaves the workflow. tool/http is decided by method
  // instead -- see kindOf.
  "tool/websearch",
  // connectors that fetch rather than send. graphql stays an action: an
  // endpoint can mutate and nothing in the node's config says whether this
  // one does. elevenlabs stays an action: it generates billable audio.
  "action/telegram_get_updates",
  "action/calendly",
  "action/openweathermap",
  "action/algorand_account",
  "action/algorand_transactions",
  "action/algorand_asset",
  "action/rss",
  "action/hackernews",
  "action/coingecko",
  "action/coingecko_history",
  // Google reads. gmail_send, gmail_reply, sheets_append and calendar_create
  // are sends and stay actions.
  "google/sheets_read",
  "google/calendar_list",
]);

/**
 * kindOf answers the question the runner asks when a node fails: may this
 * failure be degraded into an error payload the run continues with?
 *
 * Only a "read" may. "compute" is a template that cannot fail for any reason
 * outside the workflow, so its failure is an authoring bug the user needs to
 * see. Everything unlisted is "action", which fails the run -- the safe
 * answer, and the one a template added later gets until someone classifies
 * it deliberately.
 *
 * tool/http is decided by method at run time; "action" here is the safe
 * catalog answer, and the backend overrides it for a GET.
 */
function kindOf(type: string, id: string): "read" | "compute" | "action" {
  const key = `${type}/${id}`;
  if (READ_TEMPLATES.has(key)) return "read";
  if (COMPUTE_TEMPLATES.has(key)) return "compute";
  return "action";
}

export function buildNodeCatalog(): NodeCatalog {
  const types: CatalogType[] = [
    {
      type: "trigger",
      desc: "Where a run starts. Exactly one per workflow. There is no schedule/cron trigger: a timetable is a workflow-level setting (see the manual trigger).",
      templates: TRIGGER_TEMPLATES.map((t) => ({
        id: t.id,
        name: t.name,
        desc: t.desc,
        kind: kindOf("trigger", t.id),
        note: TRIGGER_NOTES[t.id],
        fields: [],
      })),
    },
    {
      type: "agent",
      desc: 'An LLM step. Needs a provider attached to its "model" port; tools attach to its "tools" port.',
      templates: AGENT_TEMPLATES.map((t) => ({
        id: t.id,
        name: t.catalogName ?? t.name,
        desc: t.catalogDesc ?? t.desc,
        kind: kindOf("agent", t.id),
        ...(AGENT_NOTES[t.id] ? { note: AGENT_NOTES[t.id] } : {}),
        fields: [
          { key: "systemPrompt", where: "field" as const, label: "System prompt", hint: "instructions the agent follows on every run" },
        ],
      })),
    },
    {
      type: "provider",
      desc: 'The model behind an agent. Only ever attached to an agent\'s "model" port, never part of the flow.',
      templates: PROVIDER_TEMPLATES.map((t) => ({
        id: t.id,
        name: t.name,
        desc: `${t.name} models`,
        kind: kindOf("provider", t.id),
        note: "Runs on the platform key by default and needs nothing from the user. Its apiKey applies only if the user switches to their own key in the Inspector -- never tell them they must supply one.",
        presets: { model: t.model },
        fields: [
          { key: "model", where: "field" as const, label: "Model", placeholder: t.model },
          { key: "keyMode", where: "field" as const, label: "Key mode", hint: '"platform" uses AgentMesh\'s key and bills credits (the default); "byok" uses the user\'s own key' },
          { key: "apiKey", where: "secret" as const, label: "API key", hint: 'only for keyMode "byok"' },
        ],
      })),
    },
    {
      type: "tool",
      desc: 'A deterministic step. Attach to an agent\'s "tools" port for the agent to call, or place directly in the flow.',
      templates: TOOL_TEMPLATES.map((t) => ({
        id: t.id,
        name: t.name,
        desc: t.desc,
        kind: kindOf("tool", t.id),
        ...(TOOL_NOTES[t.id] ? { note: TOOL_NOTES[t.id] } : {}),
        fields: TOOL_FIELDS[t.id] ?? fromConnectorTable(t.id),
      })),
    },
    {
      type: "action",
      desc: "Sends something somewhere, or fetches from a service. Usually a flow step after the agent.",
      templates: ACTION_TEMPLATES.map((t) => {
        const auth = CONNECTOR_AUTH[t.id];
        const oauth = CONNECTOR_CONFIG_FIELDS[t.id]?.oauthProvider;
        return {
          id: t.id,
          name: t.catalogName ?? t.name,
          desc: t.desc,
          kind: kindOf("action", t.id),
          ...(ACTION_NOTES[t.id] ? { note: ACTION_NOTES[t.id] } : {}),
          fields: t.id === "email" ? EMAIL_FIELDS : [...fromConnectorTable(t.id), MESSAGE_TEMPLATE],
          ...(auth ? { authDocUrl: auth.docUrl } : {}),
          ...(oauth ? { oauthProvider: oauth } : {}),
        };
      }),
    },
    {
      type: "google",
      desc: "Gmail sending, Sheets and Calendar, through one Google account the user links in the Inspector.",
      templates: GOOGLE_TEMPLATES.map((t) => ({
        id: t.id,
        name: t.name,
        desc: t.desc,
        kind: kindOf("google", t.id),
        fields: [
          GOOGLE_ACCOUNT,
          ...(GOOGLE_FIELDS[t.id] ?? []),
          ...("usesMessage" in t && t.usesMessage ? [MESSAGE_TEMPLATE] : []),
        ],
      })),
    },
    {
      type: "state",
      desc: "Values that persist between runs of this workflow (a cursor, a counter, the last price seen). Read anywhere as {{state.key}}.",
      templates: STATE_TEMPLATES.map((t) => ({
        id: t.id,
        name: t.name,
        desc: t.desc,
        kind: kindOf("state", t.id),
        presets: { stateOp: t.id },
        fields: [
          { key: "stateKey", where: "field" as const, label: "Key", hint: "persists across runs", placeholder: "lastRowId" },
          ...(t.id === "set"
            ? [{ key: "stateValue", where: "field" as const, label: "Value", hint: "blank stores the previous step's output" }]
            : t.id === "increment"
              ? [{ key: "stateValue", where: "field" as const, label: "Amount", hint: "defaults to 1" }]
              : []),
        ],
      })),
    },
    {
      type: "tendril",
      desc: "Rented Tendril compute: buy credit, rent a machine, run code on it, release it.",
      templates: TENDRIL_TEMPLATES.map((t) => ({
        id: t.id,
        name: t.name,
        desc: t.desc,
        kind: kindOf("tendril", t.id),
        presets: { tendrilAction: t.action, tendrilHours: "1", tendrilAmount: t.action === "auto" ? "" : "10" },
        fields:
          t.action === "auto"
            ? [{ key: "tendrilAmount", where: "field" as const, label: "Budget (USD, optional)", placeholder: "1" }]
            : t.action === "topup"
            ? [
                { key: "tendrilAmount", where: "field" as const, label: "Amount (USD)", placeholder: "10" },
                { key: "tendrilMinBalance", where: "field" as const, label: "Only if credit below (USD)", hint: "blank tops up on every run" },
                { key: "tendrilCoverHours", where: "field" as const, label: "Cover rent of (hours)", hint: "buys only the shortfall for the next rent, at least Amount; overrides Only if credit below" },
              ]
            : t.action === "rent"
              ? [{ key: "tendrilHours", where: "field" as const, label: "Hours", placeholder: "1" }]
              : [],
      })),
    },
    {
      type: "end",
      desc: "Where the run finishes.",
      templates: END_TEMPLATES.map((t) => ({
        id: t.id,
        name: t.name,
        desc: t.desc,
        kind: kindOf("end", t.id),
        // "Respond to Webhook" reads as if the webhook caller gets this
        // output back. It does not: the public trigger answers 202 {runId}
        // before the run even finishes (handlers/runs.go PublicTrigger).
        note: "Finishes the run and passes the last output through; the http and done templates behave identically. A webhook caller never receives run output -- the webhook answers 202 with a runId immediately -- so do not promise a response body.",
        fields: [],
      })),
    },
  ];
  return { version: 1, types };
}
