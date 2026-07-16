-- Provision every Postgres role instancez needs. Runs once on first
-- container boot via /docker-entrypoint-initdb.d. instancez's migration
-- assumes these roles already exist; in managed deployments the control
-- plane is responsible for the equivalent provisioning.

-- Login roles.
CREATE ROLE instancez_owner LOGIN PASSWORD 'instancez'
    CREATEROLE CREATEDB BYPASSRLS REPLICATION;

CREATE ROLE authenticator LOGIN PASSWORD 'instancez' NOINHERIT;

-- No-login API roles assumed by the request pool's SET LOCAL ROLE.
CREATE ROLE anon NOLOGIN;
CREATE ROLE authenticated NOLOGIN;
CREATE ROLE service_role NOLOGIN BYPASSRLS;

-- Membership: authenticator must be able to SET LOCAL ROLE into any of
-- the three API roles per request.
GRANT anon, authenticated, service_role TO authenticator;

-- Database / public schema ownership for the migration owner.
ALTER DATABASE instancez OWNER TO instancez_owner;
ALTER SCHEMA public OWNER TO instancez_owner;
GRANT ALL ON SCHEMA public TO instancez_owner;
