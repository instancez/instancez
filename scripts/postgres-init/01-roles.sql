-- Provision the two login roles ultrabase expects. Runs once on first
-- container boot via /docker-entrypoint-initdb.d. The three no-login
-- roles (anon, authenticated, service_role) are created by the
-- application's migration; only the LOGIN roles need to exist before
-- ultrabase connects.

CREATE ROLE ultrabase_owner LOGIN PASSWORD 'ultrabase'
    CREATEROLE CREATEDB BYPASSRLS REPLICATION;

CREATE ROLE authenticator LOGIN PASSWORD 'ultrabase' INHERIT;

ALTER DATABASE ultrabase OWNER TO ultrabase_owner;
ALTER SCHEMA public OWNER TO ultrabase_owner;
GRANT ALL ON SCHEMA public TO ultrabase_owner;
