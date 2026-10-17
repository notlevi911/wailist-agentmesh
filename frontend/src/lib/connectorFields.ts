// Per-connector settings and credentials, and where each credential comes
// from. Plain data with no React in it, so that two very different consumers
// can share one copy: the Inspector renders these fields for a human, and
// nodeCatalog.ts feeds the same tables to the chat workflow builder, so the
// builder knows exactly which settings a connector really has. Moved here
// verbatim from Inspector.tsx; keep it data-only.

// ── Per-connector config field tables ───────────────────────────────────────
// Catalog overrides keep UI copy edits out of the generated backend catalog.
export type ConnectorField =
  | {
      kind: "secret";
      key: string;
      label: string;
      hint?: string;
      catalogHint?: string;
      placeholder: string;
      catalogPlaceholder?: string;
      // The field's old Secrets key, for a connector whose Inspector field
      // moved to a new key -- e.g. Stripe's stripeAPIKey (was
      // stripeSecretKey). The backend already falls back to this key for a
      // node saved under the old name and runs correctly either way; this
      // is only so the "connected" status badge below doesn't call an
      // already-working node "Not connected" just because it checks the
      // new key alone.
      legacyKey?: string;
    }
  | {
      kind: "config";
      key: string;
      label: string;
      hint?: string;
      catalogHint?: string;
      placeholder?: string;
      catalogPlaceholder?: string;
    };

export const CONNECTOR_CONFIG_FIELDS: Record<
  string,
  { label: string; oauthProvider?: string; fields: ConnectorField[] }
> = {
  slack: {
    label: "Slack config",
    oauthProvider: "slack",
    fields: [
      {
        kind: "secret",
        key: "slackWebhookURL",
        label: "Webhook URL",
        hint: "or connect above for bot-token mode",
        placeholder: "https://hooks.slack.com/services/…",
      },
      {
        kind: "config",
        key: "slackChannel",
        label: "Channel ID (bot-token mode)",
        placeholder: "C0123456789",
      },
    ],
  },
  discord: {
    label: "Discord config",
    fields: [
      {
        kind: "secret",
        key: "discordWebhookURL",
        label: "Webhook URL",
        placeholder: "https://discord.com/api/webhooks/…",
      },
    ],
  },
  teams: {
    label: "Teams config",
    fields: [
      {
        kind: "secret",
        key: "teamsWebhookURL",
        label: "Webhook URL",
        placeholder: "https://…webhook.office.com/webhookb2/…",
      },
    ],
  },
  google_chat: {
    label: "Google Chat config",
    fields: [
      {
        kind: "secret",
        key: "googleChatWebhookURL",
        label: "Webhook URL",
        placeholder: "https://chat.googleapis.com/v1/spaces/…",
      },
    ],
  },
  ntfy: {
    label: "Ntfy config",
    fields: [
      {
        kind: "config",
        key: "ntfyTopic",
        label: "Topic",
        placeholder: "agentmesh-alerts",
      },
      {
        kind: "config",
        key: "ntfyServerURL",
        label: "Server URL",
        placeholder: "https://ntfy.sh (default)",
      },
      {
        kind: "secret",
        key: "ntfyAuthToken",
        label: "Auth Token",
        hint: "optional, for private topics",
        placeholder: "tk_xxxxxxxxxxxx",
      },
    ],
  },
  telegram: {
    label: "Telegram config",
    fields: [
      {
        kind: "secret",
        key: "telegramBotToken",
        label: "Bot Token",
        placeholder: "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "telegramChatID",
        label: "Chat ID",
        placeholder: "-1001234567890",
      },
    ],
  },
  telegram_get_updates: {
    label: "Telegram config",
    fields: [
      {
        kind: "secret",
        key: "telegramBotToken",
        label: "Bot Token",
        placeholder: "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "telegramOffset",
        label: "Offset",
        hint: "optional -- only updates after this ID",
        placeholder: "e.g. 481231",
      },
      {
        kind: "config",
        key: "telegramLimit",
        label: "Limit",
        hint: "optional, default 100",
        placeholder: "e.g. 20",
      },
    ],
  },
  github: {
    label: "GitHub config",
    oauthProvider: "github",
    fields: [
      {
        kind: "secret",
        key: "githubToken",
        label: "Personal Access Token",
        placeholder: "ghp_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "githubRepo",
        label: "Repository",
        placeholder: "owner/repo",
      },
    ],
  },
  notion: {
    label: "Notion config",
    oauthProvider: "notion",
    fields: [
      {
        kind: "secret",
        key: "notionAPIKey",
        label: "Internal Integration Secret",
        placeholder: "secret_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "notionPageID",
        label: "Page ID",
        placeholder: "the target page's UUID",
      },
    ],
  },
  airtable: {
    label: "Airtable config",
    oauthProvider: "airtable",
    fields: [
      {
        kind: "secret",
        key: "airtableAPIKey",
        label: "Personal Access Token",
        placeholder: "pat_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "airtableBaseID",
        label: "Base ID",
        placeholder: "appXXXXXXXXXXXXXX",
      },
      {
        kind: "config",
        key: "airtableTable",
        label: "Table",
        placeholder: "Tasks",
      },
      {
        kind: "config",
        key: "airtableFieldName",
        label: "Field Name",
        placeholder: "Notes (default)",
      },
    ],
  },
  hubspot: {
    label: "HubSpot config",
    oauthProvider: "hubspot",
    fields: [
      {
        kind: "secret",
        key: "hubspotAPIKey",
        label: "Private App Token",
        placeholder: "pat-na1-xxxxxxxxxxxxxxxxxxxx",
      },
    ],
  },
  trello: {
    label: "Trello config",
    fields: [
      {
        kind: "secret",
        key: "trelloAPIKey",
        label: "API Key",
        placeholder: "your Trello API key",
      },
      {
        kind: "secret",
        key: "trelloToken",
        label: "Token",
        placeholder: "your Trello token",
      },
      {
        kind: "config",
        key: "trelloListID",
        label: "List ID",
        placeholder: "target list id",
      },
    ],
  },
  asana: {
    label: "Asana config",
    oauthProvider: "asana",
    fields: [
      {
        kind: "secret",
        key: "asanaAPIKey",
        label: "Personal Access Token",
        placeholder: "1/1234567890:xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "asanaProjectID",
        label: "Project ID",
        placeholder: "target project id",
      },
    ],
  },
  clickup: {
    label: "ClickUp config",
    oauthProvider: "clickup",
    fields: [
      {
        kind: "secret",
        key: "clickupAPIKey",
        label: "API Token",
        placeholder: "pk_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "clickupListID",
        label: "List ID",
        placeholder: "target list id",
      },
    ],
  },
  jira: {
    label: "Jira config",
    oauthProvider: "jira",
    fields: [
      {
        kind: "secret",
        key: "jiraAPIToken",
        label: "API Token",
        placeholder: "your Atlassian API token",
      },
      {
        kind: "config",
        key: "jiraEmail",
        label: "Account Email",
        placeholder: "bot@yourcompany.com",
      },
      {
        kind: "config",
        key: "jiraDomain",
        label: "Site Domain",
        placeholder: "yourcompany (as in yourcompany.atlassian.net)",
      },
      {
        kind: "config",
        key: "jiraProjectKey",
        label: "Project Key",
        placeholder: "ENG",
      },
      {
        kind: "config",
        key: "jiraIssueType",
        label: "Issue Type",
        placeholder: "Task (default)",
      },
    ],
  },
  mailchimp: {
    label: "Mailchimp config",
    oauthProvider: "mailchimp",
    fields: [
      {
        kind: "secret",
        key: "mailchimpAPIKey",
        label: "API Key",
        placeholder: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx-us21",
      },
      {
        kind: "config",
        key: "mailchimpListID",
        label: "Audience (List) ID",
        placeholder: "target list id",
      },
      {
        kind: "config",
        key: "mailchimpEmail",
        label: "Email",
        hint: "optional, defaults to the run's output",
        placeholder: "leave blank to use the agent's message as the email",
      },
    ],
  },
  linear: {
    label: "Linear config",
    oauthProvider: "linear",
    fields: [
      {
        kind: "secret",
        key: "linearAPIKey",
        label: "Personal API Key",
        placeholder: "lin_api_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "linearTeamID",
        label: "Team ID",
        placeholder: "target team id",
      },
    ],
  },
  todoist: {
    label: "Todoist config",
    oauthProvider: "todoist",
    fields: [
      {
        kind: "secret",
        key: "todoistAPIKey",
        label: "API Token",
        placeholder: "your Todoist API token",
      },
      {
        kind: "config",
        key: "todoistProjectID",
        label: "Project ID",
        hint: "optional",
        placeholder: "leave blank for Inbox",
      },
    ],
  },
  gitlab: {
    label: "GitLab config",
    oauthProvider: "gitlab",
    fields: [
      {
        kind: "secret",
        key: "gitlabAPIToken",
        label: "Personal Access Token",
        placeholder: "glpat-xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "gitlabProjectID",
        label: "Project ID",
        placeholder: "numeric project id",
      },
      {
        kind: "config",
        key: "gitlabBaseURL",
        label: "Base URL",
        hint: "optional, for self-hosted",
        placeholder: "https://gitlab.com (default)",
      },
    ],
  },
  sentry: {
    label: "Sentry config",
    fields: [
      {
        kind: "secret",
        key: "sentryDSN",
        label: "DSN",
        placeholder: "https://xxxx@o000000.ingest.sentry.io/000000",
      },
    ],
  },
  supabase: {
    label: "Supabase config",
    fields: [
      {
        kind: "secret",
        key: "supabaseAPIKey",
        label: "Service Role Key",
        placeholder: "eyJhbGciOi…",
      },
      {
        kind: "config",
        key: "supabaseProjectURL",
        label: "Project URL",
        placeholder: "https://xxxxxxxx.supabase.co",
      },
      {
        kind: "config",
        key: "supabaseTable",
        label: "Table",
        placeholder: "logs",
      },
      {
        kind: "config",
        key: "supabaseColumn",
        label: "Column",
        placeholder: "content (default)",
      },
    ],
  },
  woocommerce: {
    label: "WooCommerce config",
    fields: [
      {
        kind: "secret",
        key: "woocommerceConsumerKey",
        label: "Consumer Key",
        placeholder: "ck_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "secret",
        key: "woocommerceConsumerSecret",
        label: "Consumer Secret",
        placeholder: "cs_xxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "woocommerceStoreURL",
        label: "Store URL",
        placeholder: "https://yourstore.com",
      },
      {
        kind: "config",
        key: "woocommerceOrderID",
        label: "Order ID",
        placeholder: "target order id",
      },
    ],
  },
  elevenlabs: {
    label: "ElevenLabs config",
    fields: [
      {
        kind: "secret",
        key: "elevenlabsAPIKey",
        label: "API Key",
        placeholder: "your ElevenLabs API key",
      },
      {
        kind: "config",
        key: "elevenlabsVoiceID",
        label: "Voice ID",
        placeholder: "21m00Tcm4TlvDq8ikWAM (Rachel, default)",
      },
    ],
  },
  set: {
    label: "Edit Fields config",
    fields: [
      {
        kind: "config",
        key: "setFields",
        label: "Fields (JSON)",
        placeholder: '{"city":"{{ node.n1.city }}","asked":"{{ input }}"}',
        hint: "String values may use {{ result }}, {{ input }}, {{ node.<id>.<field> }}",
      },
    ],
  },
  json_extract: {
    label: "JSON Extract config",
    fields: [
      {
        kind: "config",
        key: "jsonPath",
        label: "Path",
        placeholder: "data.items.0.name",
        hint: "Dot path; numeric segments index arrays",
      },
    ],
  },
  crypto: {
    label: "Crypto config",
    fields: [
      {
        kind: "config",
        key: "cryptoAction",
        label: "Action",
        placeholder: "sha256",
        hint: "sha256 · sha512 · sha1 · md5 · hmac-sha256 · base64 · base64decode",
      },
      {
        kind: "secret",
        key: "cryptoSecret",
        label: "HMAC secret",
        hint: "only for hmac-sha256",
        placeholder: "shared secret",
      },
    ],
  },
  datetime: {
    label: "Date & Time config",
    fields: [
      {
        kind: "config",
        key: "dtFormat",
        label: "Format",
        placeholder: "rfc3339",
        hint: "rfc3339 · unix · date · time · or a Go layout",
      },
      {
        kind: "config",
        key: "dtOffset",
        label: "Offset",
        hint: "optional",
        placeholder: "-24h",
      },
      {
        kind: "config",
        key: "dtZone",
        label: "Timezone",
        hint: "optional, IANA name",
        placeholder: "Asia/Kolkata",
      },
    ],
  },
  template: {
    label: "Text Template config",
    fields: [
      {
        kind: "config",
        key: "templateText",
        label: "Template",
        placeholder: "Result: {{ result }}",
        hint: "Supports {{ result }}, {{ input }}, {{ node.<id>.<field> }}",
      },
    ],
  },
  stripe: {
    label: "Stripe config",
    fields: [
      {
        kind: "secret",
        key: "stripeAPIKey",
        label: "Secret Key",
        placeholder: "sk_live_xxxxxxxxxxxx",
        legacyKey: "stripeSecretKey",
      },
      {
        kind: "config",
        key: "stripeEmail",
        label: "Customer email",
        placeholder: "buyer@example.com",
      },
      {
        kind: "config",
        key: "stripeName",
        label: "Customer name",
        hint: "optional",
        placeholder: "leave blank to omit",
      },
    ],
  },
  twilio: {
    label: "Twilio config",
    fields: [
      {
        kind: "secret",
        key: "twilioAuthToken",
        label: "Auth Token",
        placeholder: "your Twilio auth token",
      },
      {
        kind: "config",
        key: "twilioAccountSID",
        label: "Account SID",
        placeholder: "ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "twilioFrom",
        label: "From",
        placeholder: "+15551234567 (a number on your account)",
      },
      {
        kind: "config",
        key: "twilioTo",
        label: "To",
        placeholder: "+15559876543",
      },
    ],
  },
  mattermost: {
    label: "Mattermost config",
    fields: [
      {
        kind: "secret",
        key: "mattermostWebhookURL",
        label: "Incoming Webhook URL",
        placeholder: "https://mattermost.example.com/hooks/xxx",
      },
      {
        kind: "config",
        key: "mattermostChannel",
        label: "Channel",
        hint: "optional",
        placeholder: "town-square",
      },
      {
        kind: "config",
        key: "mattermostUsername",
        label: "Post as",
        hint: "optional",
        placeholder: "AgentMesh",
      },
    ],
  },
  pagerduty: {
    label: "PagerDuty config",
    fields: [
      {
        kind: "secret",
        key: "pagerdutyRoutingKey",
        label: "Events API v2 Integration Key",
        placeholder: "32-character routing key",
      },
      {
        kind: "config",
        key: "pagerdutySeverity",
        label: "Severity",
        placeholder: "info (default)",
        hint: "critical · error · warning · info",
      },
      {
        kind: "config",
        key: "pagerdutySource",
        label: "Source",
        hint: "optional",
        placeholder: "agentmesh",
      },
    ],
  },
  zendesk: {
    label: "Zendesk config",
    fields: [
      {
        kind: "secret",
        key: "zendeskAPIToken",
        label: "API Token",
        placeholder: "your Zendesk API token",
      },
      {
        kind: "config",
        key: "zendeskSubdomain",
        label: "Subdomain",
        hint: "the part before .zendesk.com",
        placeholder: "yourcompany",
      },
      {
        kind: "config",
        key: "zendeskEmail",
        label: "Agent Email",
        placeholder: "agent@yourcompany.com",
      },
    ],
  },
  monday: {
    label: "Monday.com config",
    fields: [
      {
        kind: "secret",
        key: "mondayAPIKey",
        label: "API Token",
        placeholder: "your Monday.com v2 token",
      },
      {
        kind: "config",
        key: "mondayBoardID",
        label: "Board ID",
        placeholder: "123456789",
      },
    ],
  },
  intercom: {
    label: "Intercom config",
    fields: [
      {
        kind: "secret",
        key: "intercomAccessToken",
        label: "Access Token",
        placeholder: "your Intercom access token",
      },
      {
        kind: "config",
        key: "intercomEmail",
        label: "Lead Email",
        hint: "optional, defaults to the upstream message",
      },
    ],
  },
  openweathermap: {
    label: "OpenWeatherMap config",
    fields: [
      {
        kind: "secret",
        key: "openWeatherAPIKey",
        label: "API Key",
        placeholder: "your OpenWeatherMap API key",
      },
      {
        kind: "config",
        key: "weatherCity",
        label: "City",
        hint: "optional, defaults to the upstream message",
        placeholder: "London",
      },
      {
        kind: "config",
        key: "weatherUnits",
        label: "Units",
        placeholder: "metric (default) · imperial · standard",
      },
    ],
  },
  calendly: {
    label: "Calendly config",
    fields: [
      {
        kind: "secret",
        key: "calendlyAccessToken",
        label: "Personal Access Token",
        placeholder: "your Calendly PAT",
      },
      {
        kind: "config",
        key: "calendlyUserURI",
        label: "User URI",
        placeholder: "https://api.calendly.com/users/…",
      },
      {
        kind: "config",
        key: "calendlyCount",
        label: "Count",
        placeholder: "10 (default)",
      },
    ],
  },
  shopify_customer: {
    label: "Shopify config",
    fields: [
      {
        kind: "secret",
        key: "shopifyAccessToken",
        label: "Admin API Access Token",
        placeholder: "shpat_xxxxxxxxxxxx",
      },
      {
        kind: "config",
        key: "shopifyStore",
        label: "Store handle",
        placeholder: "acme-store (from acme-store.myshopify.com)",
      },
      {
        kind: "config",
        key: "shopifyEmail",
        label: "Customer email",
        placeholder: "buyer@example.com",
      },
    ],
  },
  pipedrive: {
    label: "Pipedrive config",
    fields: [
      {
        kind: "secret",
        key: "pipedriveAPIToken",
        label: "API Token",
        placeholder: "your Pipedrive API token",
      },
      {
        kind: "config",
        key: "pipedriveCompanyDomain",
        label: "Company domain",
        placeholder: "acme (from acme.pipedrive.com)",
      },
      {
        kind: "config",
        key: "pipedriveDealID",
        label: "Deal ID",
        hint: "optional",
        placeholder: "attach the note to a deal",
      },
      {
        kind: "config",
        key: "pipedrivePersonID",
        label: "Person ID",
        hint: "optional",
        placeholder: "attach the note to a person",
      },
    ],
  },
  db: {
    label: "Postgres config",
    fields: [
      {
        kind: "secret",
        key: "pgConnString",
        label: "Connection string",
        placeholder: "postgres://user:pass@host:5432/dbname",
      },
      {
        kind: "config",
        key: "pgTable",
        label: "Table",
        placeholder: "events",
      },
      {
        kind: "config",
        key: "pgColumn",
        label: "Output column",
        placeholder: "payload",
        hint: "receives the run output",
      },
      {
        kind: "config",
        key: "pgExtraColumns",
        label: "Extra columns (JSON)",
        hint: "optional",
        placeholder: '{"source":"agentmesh","city":"{{ node.n1.city }}"}',
      },
    ],
  },
  html_extract: {
    label: "HTML Extract config",
    fields: [
      {
        kind: "config",
        key: "htmlSelector",
        label: "CSS selector",
        placeholder: "h1.title",
      },
      {
        kind: "config",
        key: "htmlAttr",
        label: "Attribute",
        hint: "optional, blank = text",
        placeholder: "href",
      },
      {
        kind: "config",
        key: "htmlMode",
        label: "Mode",
        placeholder: "first",
        hint: "first · all",
      },
    ],
  },
  markdown: {
    label: "Markdown config",
    fields: [
      {
        kind: "config",
        key: "mdGFM",
        label: "GitHub Flavored",
        placeholder: "true",
        hint: "true · false: tables, strikethrough, autolinks",
        catalogHint: "true · false \u2014 tables, strikethrough, autolinks",
      },
    ],
  },
  rss: {
    label: "RSS config",
    fields: [
      {
        kind: "config",
        key: "rssURL",
        label: "Feed URL",
        placeholder: "https://example.com/feed.xml",
      },
      {
        kind: "config",
        key: "rssLimit",
        label: "Max items",
        hint: "optional, default 10",
        placeholder: "10",
      },
    ],
  },
  graphql: {
    label: "GraphQL config",
    fields: [
      {
        kind: "config",
        key: "graphqlEndpoint",
        label: "Endpoint",
        placeholder: "https://api.github.com/graphql",
      },
      {
        kind: "config",
        key: "graphqlQuery",
        label: "Query",
        placeholder: "query { viewer { login } }",
      },
      {
        kind: "config",
        key: "graphqlVariables",
        label: "Variables (JSON)",
        hint: "optional",
        placeholder: '{"first":10,"search":"{{ result }}"}',
      },
      {
        kind: "secret",
        key: "graphqlAuthHeader",
        label: "Authorization header",
        hint: "Sent as entered. Include Bearer if the API requires it.",
        catalogHint: "sent verbatim \u2014 include Bearer if the API wants it",
        placeholder: "Bearer ghp_xxxxxxxx",
      },
    ],
  },
  algorand_account: {
    label: "Wallet Account config",
    fields: [
      {
        kind: "config",
        key: "algoAddress",
        label: "Address",
        placeholder: "58-character address",
        catalogPlaceholder: "58-character Algorand address",
      },
    ],
  },
  algorand_transactions: {
    label: "Wallet History config",
    fields: [
      {
        kind: "config",
        key: "algoAddress",
        label: "Address",
        placeholder: "58-character address",
        catalogPlaceholder: "58-character Algorand address",
      },
      {
        kind: "config",
        key: "algoTxLimit",
        label: "How many",
        hint: "newest first; 10 by default, 50 at most",
        placeholder: "10",
      },
      {
        kind: "config",
        key: "algoTxType",
        label: "Only this type",
        hint: "blank for every type; pay, axfer, appl, acfg, afrz, keyreg, stpf, hb",
        placeholder: "axfer",
      },
    ],
  },
  algorand_asset: {
    label: "Asset Details config",
    fields: [
      {
        kind: "config",
        key: "algoAssetId",
        label: "Asset id",
        hint: "the ASA's number, not its ticker",
        placeholder: "31566704",
      },
    ],
  },
  hackernews: {
    label: "Hacker News config",
    fields: [
      {
        kind: "config",
        key: "hnQuery",
        label: "Search query",
        placeholder: "{{ result }}",
      },
      {
        kind: "config",
        key: "hnTags",
        label: "Tags",
        hint: "optional",
        placeholder: "story · comment · show_hn · ask_hn",
      },
      {
        kind: "config",
        key: "hnLimit",
        label: "Max items",
        hint: "optional, default 10",
        placeholder: "10",
      },
    ],
  },
  coingecko: {
    label: "CoinGecko config",
    fields: [
      {
        kind: "config",
        key: "cgIDs",
        label: "Coin IDs",
        placeholder: "bitcoin,ethereum",
      },
      {
        kind: "config",
        key: "cgCurrencies",
        label: "Currencies",
        hint: "optional, default usd",
        placeholder: "usd,eur",
      },
    ],
  },
  coingecko_history: {
    label: "CoinGecko History config",
    fields: [
      {
        kind: "config",
        key: "cgID",
        label: "Coin ID",
        placeholder: "bitcoin",
      },
      {
        kind: "config",
        key: "cgDays",
        label: "Days",
        hint: "optional, default 30",
        placeholder: "28",
      },
      {
        kind: "config",
        key: "cgCurrency",
        label: "Currency",
        hint: "optional, default usd",
        placeholder: "usd",
      },
    ],
  },
  quickchart: {
    label: "QuickChart config",
    fields: [
      {
        kind: "config",
        key: "qcConfig",
        label: "Chart.js config (JSON)",
        placeholder: '{"type":"bar","data":{"labels":["a","b"],"datasets":[{"data":[1,2]}]}}',
      },
      {
        kind: "config",
        key: "qcWidth",
        label: "Width",
        hint: "optional",
        placeholder: "600",
      },
      {
        kind: "config",
        key: "qcHeight",
        label: "Height",
        hint: "optional",
        placeholder: "400",
      },
    ],
  },
  // Distinct from "shopify_customer" above (which creates a customer): this
  // adds a note to an existing order, and keeps template id "shopify" --
  // master's original id and behavior for this operation -- rather than
  // "shopify_customer"'s newer id, so an already-saved order-note node
  // keeps hitting the same backend dispatch with no config change on the
  // user's side. See connectors_business.go's sendShopifyOrderNote doc
  // comment.
  shopify: {
    label: "Shopify: Add Order Note config",
    fields: [
      {
        kind: "secret",
        key: "shopifyAccessToken",
        label: "Admin API Access Token",
        placeholder: "shpat_…",
      },
      {
        kind: "config",
        key: "shopifyShopDomain",
        label: "Shop Domain",
        placeholder: "mystore.myshopify.com",
      },
      {
        kind: "config",
        key: "shopifyOrderID",
        label: "Order ID",
        placeholder: "target order id",
      },
    ],
  },
  baserow: {
    label: "Baserow config",
    fields: [
      {
        kind: "secret",
        key: "baserowAPIToken",
        label: "API Token",
        placeholder: "your Baserow database token",
      },
      {
        kind: "config",
        key: "baserowTableID",
        label: "Table ID",
        placeholder: "the numeric table id",
      },
      {
        kind: "config",
        key: "baserowFieldName",
        label: "Field Name",
        placeholder: "Notes (default)",
      },
    ],
  },
};

// ── Per-connector auth metadata ─────────────────────────────────────────────
// Where each connector's credential is obtained. Every live connector requires
// an account login to get its credential EXCEPT ntfy (token is optional), which
// is why it alone carries needsLogin: false.
export const CONNECTOR_AUTH: Record<
  string,
  { needsLogin: boolean; docUrl: string; linkLabel: string }
> = {
  slack: {
    needsLogin: true,
    docUrl: "https://api.slack.com/apps",
    linkLabel: "Create webhook",
  },
  discord: {
    needsLogin: true,
    docUrl:
      "https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks",
    linkLabel: "Create webhook",
  },
  teams: {
    needsLogin: true,
    docUrl:
      "https://learn.microsoft.com/microsoftteams/platform/webhooks-and-connectors/how-to/add-incoming-webhook",
    linkLabel: "Create webhook",
  },
  google_chat: {
    needsLogin: true,
    docUrl: "https://developers.google.com/workspace/chat/quickstart/webhooks",
    linkLabel: "Create webhook",
  },
  ntfy: {
    needsLogin: false,
    docUrl: "https://docs.ntfy.sh/publish/",
    linkLabel: "ntfy docs",
  },
  telegram: {
    needsLogin: true,
    docUrl: "https://t.me/BotFather",
    linkLabel: "Open BotFather",
  },
  telegram_get_updates: {
    needsLogin: true,
    docUrl: "https://t.me/BotFather",
    linkLabel: "Open BotFather",
  },
  github: {
    needsLogin: true,
    docUrl: "https://github.com/settings/tokens",
    linkLabel: "Get token",
  },
  notion: {
    needsLogin: true,
    docUrl: "https://www.notion.so/my-integrations",
    linkLabel: "Get secret",
  },
  airtable: {
    needsLogin: true,
    docUrl: "https://airtable.com/create/tokens",
    linkLabel: "Get token",
  },
  hubspot: {
    needsLogin: true,
    docUrl: "https://app.hubspot.com/private-apps",
    linkLabel: "Get token",
  },
  trello: {
    needsLogin: true,
    docUrl: "https://trello.com/power-ups/admin",
    linkLabel: "Get key & token",
  },
  asana: {
    needsLogin: true,
    docUrl: "https://app.asana.com/0/my-apps",
    linkLabel: "Get token",
  },
  clickup: {
    needsLogin: true,
    docUrl: "https://app.clickup.com/settings/apps",
    linkLabel: "Get token",
  },
  jira: {
    needsLogin: true,
    docUrl: "https://id.atlassian.com/manage-profile/security/api-tokens",
    linkLabel: "Get token",
  },
  mailchimp: {
    needsLogin: true,
    docUrl: "https://admin.mailchimp.com/account/api/",
    linkLabel: "Get key",
  },
  linear: {
    needsLogin: true,
    docUrl: "https://linear.app/settings/api",
    linkLabel: "Get key",
  },
  todoist: {
    needsLogin: true,
    docUrl: "https://todoist.com/app/settings/integrations/developer",
    linkLabel: "Get token",
  },
  gitlab: {
    needsLogin: true,
    docUrl: "https://gitlab.com/-/user_settings/personal_access_tokens",
    linkLabel: "Get token",
  },
  sentry: {
    needsLogin: true,
    docUrl:
      "https://docs.sentry.io/product/sentry-basics/concepts/dsn-explainer/",
    linkLabel: "Find your DSN",
  },
  supabase: {
    needsLogin: true,
    docUrl: "https://supabase.com/dashboard/project/_/settings/api",
    linkLabel: "Get service key",
  },
  woocommerce: {
    needsLogin: true,
    docUrl: "https://woocommerce.com/document/woocommerce-rest-api/",
    linkLabel: "Get API keys",
  },
  elevenlabs: {
    needsLogin: true,
    docUrl: "https://elevenlabs.io/app/settings/api-keys",
    linkLabel: "Get key",
  },
  twilio: {
    needsLogin: true,
    docUrl: "https://console.twilio.com",
    linkLabel: "Get credentials",
  },
  stripe: {
    needsLogin: true,
    docUrl: "https://dashboard.stripe.com/apikeys",
    linkLabel: "Get key",
  },
  pagerduty: {
    needsLogin: true,
    docUrl: "https://support.pagerduty.com/docs/services-and-integrations",
    linkLabel: "Get integration key",
  },
  zendesk: {
    needsLogin: true,
    docUrl: "https://support.zendesk.com/hc/en-us/articles/4408889192858",
    linkLabel: "Get API token",
  },
  intercom: {
    needsLogin: true,
    docUrl: "https://app.intercom.com/a/apps/_/settings/api-keys",
    linkLabel: "Get token",
  },
  openweathermap: {
    needsLogin: true,
    docUrl: "https://home.openweathermap.org/api_keys",
    linkLabel: "Get key",
  },
  calendly: {
    needsLogin: true,
    docUrl: "https://calendly.com/integrations/api_webhooks",
    linkLabel: "Get token",
  },
  shopify: {
    needsLogin: true,
    docUrl:
      "https://shopify.dev/docs/apps/build/authentication-authorization/access-tokens",
    linkLabel: "Get access token",
  },
  shopify_customer: {
    needsLogin: true,
    docUrl: "https://shopify.dev/docs/apps/build/authentication-authorization/access-tokens",
    linkLabel: "Get access token",
  },
  baserow: {
    needsLogin: true,
    docUrl: "https://baserow.io/user/settings/tokens",
    linkLabel: "Get token",
  },
};
