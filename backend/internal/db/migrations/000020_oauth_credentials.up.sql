-- oauth_credentials stores a per-user, per-provider OAuth2 connection (e.g.
-- a Google account) that Google/Slack/Microsoft-style connector nodes read
-- a live access token from. Distinct from the existing GOOGLE_CLIENT_ID/
-- SECRET login flow (backend/internal/api/handlers/oauth.go) -- that flow
-- authenticates a person into AgentMesh and never persists a token; this
-- table persists real, refreshable tokens a workflow node calls Google's
-- own APIs with, on the user's behalf, potentially long after the browser
-- session that connected it is gone.
CREATE TABLE oauth_credentials (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    -- account_label is a human-readable hint (e.g. the connected Google
    -- account's email) so the Inspector's credential picker can show which
    -- account a saved connection is, without ever displaying the token.
    account_label TEXT NOT NULL DEFAULT '',
    access_token_enc TEXT NOT NULL,
    -- refresh_token_enc can be '' -- a provider only issues one on the
    -- FIRST consent with offline access; a re-consent may omit it, in
    -- which case the existing refresh token (if any) must be preserved,
    -- not overwritten with empty.
    refresh_token_enc TEXT NOT NULL DEFAULT '',
    scopes TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_oauth_credentials_user_provider ON oauth_credentials(user_id, provider);
