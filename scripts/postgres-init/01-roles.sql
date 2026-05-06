-- Provision every Postgres role ultrabase needs. Runs once on first
-- container boot via /docker-entrypoint-initdb.d. ultrabase's migration
-- assumes these roles already exist; in managed deployments the control
-- plane is responsible for the equivalent provisioning.

-- Login roles.
CREATE ROLE ultrabase_owner LOGIN PASSWORD 'ultrabase'
    CREATEROLE CREATEDB BYPASSRLS REPLICATION;

CREATE ROLE authenticator LOGIN PASSWORD 'ultrabase' INHERIT;

-- No-login API roles assumed by the request pool's SET LOCAL ROLE.
CREATE ROLE anon NOLOGIN;
CREATE ROLE authenticated NOLOGIN;
CREATE ROLE service_role NOLOGIN BYPASSRLS;

-- Membership: authenticator must be able to SET LOCAL ROLE into any of
-- the three API roles per request.
GRANT anon, authenticated, service_role TO authenticator;

-- Database / public schema ownership for the migration owner.
ALTER DATABASE ultrabase OWNER TO ultrabase_owner;
ALTER SCHEMA public OWNER TO ultrabase_owner;
GRANT ALL ON SCHEMA public TO ultrabase_owner;
