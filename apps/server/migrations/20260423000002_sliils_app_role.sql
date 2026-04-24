-- +goose Up
-- Create the non-owner runtime role used for RLS enforcement.
--
-- Background: Postgres superusers (including the default `postgres`) have the
-- BYPASSRLS attribute, which silently bypasses every row-level-security policy
-- — including tables marked FORCE ROW LEVEL SECURITY. The only way to get
-- enforcement is to run app queries as a non-superuser role that lacks
-- BYPASSRLS and does not own the tables. This migration creates that role.
--
-- At runtime the app connects as whatever the DSN specifies (typically the
-- migration user) and immediately runs `SET ROLE sliils_app` on every new
-- connection (see apps/server/internal/db/db.go). Migrations themselves run
-- under the DSN user (table owner) so DDL still works.
--
-- Idempotent: safe to re-run. The role is only created if missing, and the
-- GRANT statements are already idempotent.

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'sliils_app') THEN
        -- NOLOGIN: this role is only ever reached via `SET ROLE`, never used
        -- for direct authentication. INHERIT is the default but we're explicit.
        -- NOBYPASSRLS is the default for non-superusers but also explicit.
        CREATE ROLE sliils_app NOLOGIN INHERIT NOBYPASSRLS;
    END IF;
END$$;
-- +goose StatementEnd

-- Grant DML on everything currently in the public schema and every future
-- table / sequence. Migrations continue to own new tables; this GRANT makes
-- them reachable from sliils_app without transferring ownership.
GRANT USAGE ON SCHEMA public TO sliils_app;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES    IN SCHEMA public TO sliils_app;
GRANT USAGE, SELECT                  ON ALL SEQUENCES IN SCHEMA public TO sliils_app;
GRANT EXECUTE                        ON ALL FUNCTIONS IN SCHEMA public TO sliils_app;

-- Future tables / sequences / functions created by the migration user are
-- automatically granted to sliils_app. Must be set per-migration-user.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES    TO sliils_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT                  ON SEQUENCES TO sliils_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT EXECUTE                        ON FUNCTIONS TO sliils_app;

-- +goose Down
-- +goose StatementBegin
-- Revoke everything, then drop the role. Order matters: dependent objects
-- first, then the role itself.
REVOKE ALL ON ALL TABLES    IN SCHEMA public FROM sliils_app;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM sliils_app;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA public FROM sliils_app;
REVOKE ALL ON SCHEMA public FROM sliils_app;

ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES    FROM sliils_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE USAGE, SELECT                  ON SEQUENCES FROM sliils_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE EXECUTE                        ON FUNCTIONS FROM sliils_app;

DROP ROLE IF EXISTS sliils_app;
-- +goose StatementEnd
