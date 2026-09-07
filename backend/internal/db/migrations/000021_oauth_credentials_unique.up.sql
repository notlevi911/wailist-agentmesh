-- Reconnecting the same account (same user, provider, and account_label)
-- previously inserted a brand-new row every time instead of replacing the
-- old one, leaving stale rows with still-valid refresh tokens behind and
-- filling the Inspector's credential picker with duplicate-looking entries.
-- This constraint lets OAuth2CredCallback upsert in place instead.
ALTER TABLE oauth_credentials
    ADD CONSTRAINT oauth_credentials_user_provider_label_key
    UNIQUE (user_id, provider, account_label);
