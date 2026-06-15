# Helm Chart Design

**Date:** 2026-06-15
**Topic:** Kubernetes deployment via Helm, including bundled PostgreSQL

## Summary

Add a Helm chart at `helm/instancez/` to support Kubernetes deployments. The chart deploys the `inz serve` backend and an optional vanilla PostgreSQL StatefulSet. Role provisioning is moved into the CLI startup sequence so it works uniformly across all deployment modes (dev, prod, K8s, bare metal). The operator only ever supplies one database credential: a superuser connection URL.

---

## 1. Chart Location

`helm/instancez/` in this repo. Chart versions track the app release.

---

## 2. Chart Structure

```
helm/instancez/
├── Chart.yaml
├── values.yaml
├── .helmignore
└── templates/
    ├── _helpers.tpl
    ├── NOTES.txt
    ├── secret.yaml
    ├── configmap.yaml
    ├── deployment.yaml
    ├── service.yaml
    ├── ingress.yaml
    ├── postgres-statefulset.yaml   # conditional on postgres.enabled
    └── postgres-service.yaml       # conditional on postgres.enabled
```

---

## 3. Values Schema

```yaml
image:
  repository: ghcr.io/instancez/instancez
  tag: ""            # defaults to Chart.AppVersion
  pullPolicy: IfNotPresent

replicaCount: 1

# Sensitive values — auto-generated 32-char random strings if empty (see Secret section)
adminKey: ""
jwtSecret: ""

# Raw instancez.yaml block — written to a ConfigMap and mounted at /app/instancez.yaml
config: |
  version: 1
  project:
    name: instancez
  server:
    port: 8080

service:
  type: ClusterIP
  port: 80

ingress:
  enabled: false
  className: ""
  annotations: {}   # user supplies their own (nginx, traefik, etc.)
  host: ""
  tls: []           # list of { hosts: [...], secretName: "..." }

postgres:
  enabled: true
  image: postgres:17-alpine
  database: instancez
  username: postgres
  password: ""      # auto-generated 32-char random string if empty
  storage:
    size: 10Gi
    storageClass: ""
  resources: {}

# Used when postgres.enabled=false
externalPostgres:
  url: ""           # full superuser DSN: postgres://user:pass@host:5432/db

resources: {}
nodeSelector: {}
tolerations: []
affinity: {}
```

---

## 4. Kubernetes Secret

The chart creates one Secret containing three keys:

| Key | Source |
|-----|--------|
| `adminKey` | `values.adminKey` or auto-generated 32-char string |
| `jwtSecret` | `values.jwtSecret` or auto-generated 32-char string |
| `databaseUrl` | Auto-derived (see below) or `externalPostgres.url` |

**Auto-generation (adminKey, jwtSecret, postgres.password):** Uses Helm's `lookup` function to preserve existing values across `helm upgrade`. Logic per value:
1. Explicit value in `values.yaml` → use it
2. Secret already exists in cluster (upgrade) → reuse existing value
3. First install, nothing provided → `randAlphaNum 32`

**`databaseUrl` construction:**
- `postgres.enabled=true`: `postgres://<postgres.username>:<postgres.password>@<release>-postgres:5432/<postgres.database>?sslmode=disable`
- `postgres.enabled=false`: taken directly from `externalPostgres.url`

In both cases the Deployment references `databaseUrl` from the Secret uniformly — no branching in the Deployment template.

---

## 5. PostgreSQL StatefulSet (postgres.enabled=true)

**`postgres-statefulset.yaml`:**
- Image: `postgres:17-alpine` (vanilla, no custom init scripts)
- Env: `POSTGRES_USER`, `POSTGRES_DB` from values; `POSTGRES_PASSWORD` from Secret
- Storage: `volumeClaimTemplates` with configurable size and storageClass
- Readiness probe: `pg_isready -U <username>`

**`postgres-service.yaml`:**
- ClusterIP Service named `<release>-postgres` on port 5432
- This hostname is baked into the auto-derived `databaseUrl`

No init ConfigMap. The vanilla postgres image creates the superuser and database on first boot. All instancez-specific role provisioning is handled by the CLI on startup.

---

## 6. CLI Role Provisioning

**This is a Go code change** to `internal/app/` that runs in both `inz serve` and `inz dev`, before migrations, on every startup.

### Inputs

Only `INSTANCEZ_DATABASE_URL` is required as an external credential. `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` are **removed as external inputs** and derived internally.

### Startup Sequence

1. Parse `INSTANCEZ_DATABASE_URL` — extract host, port, dbname, username, **password**
2. Open a superuser connection
3. Run idempotent provisioning (DO $$ block):

```sql
-- Login roles
CREATE ROLE IF NOT EXISTS instancez_owner LOGIN CREATEROLE CREATEDB BYPASSRLS REPLICATION;
ALTER ROLE instancez_owner PASSWORD '<parsed>';

CREATE ROLE IF NOT EXISTS authenticator LOGIN NOINHERIT;
ALTER ROLE authenticator PASSWORD '<parsed>';

-- No-login API roles
CREATE ROLE IF NOT EXISTS anon NOLOGIN;
CREATE ROLE IF NOT EXISTS authenticated NOLOGIN;
CREATE ROLE IF NOT EXISTS service_role NOLOGIN BYPASSRLS;

-- Grants
GRANT anon, authenticated, service_role TO authenticator;

-- Ownership
ALTER DATABASE <db> OWNER TO instancez_owner;
ALTER SCHEMA public OWNER TO instancez_owner;
GRANT ALL ON SCHEMA public TO instancez_owner;
```

4. Close superuser connection
5. Build internal pool URLs by substituting role name into the superuser URL (same password, host, port, db):
   - Owner pool: `postgres://instancez_owner:<password>@<host>:<port>/<db>`
   - Auth pool: `postgres://authenticator:<password>@<host>:<port>/<db>`
6. Proceed with migrations (owner pool), then start serving (auth pool)

### Password Behaviour

All provisioned login roles use the **same password as the superuser**. This means:
- Password rotation: update `INSTANCEZ_DATABASE_URL` (and the K8s Secret), restart — provisioning syncs all role passwords on next startup
- Rolling restarts: safe, password doesn't change between restarts
- Multi-replica: safe, all pods use the same password

### Role Name Configuration

Role names remain configurable via `INSTANCEZ_DB_*_ROLE` env vars. The provisioning SQL uses those configured names, not hardcoded strings.

### Password Sync Edge Cases

| Situation | Result |
|---|---|
| Role doesn't exist | `CREATE ROLE IF NOT EXISTS` + `ALTER ROLE ... PASSWORD` |
| Role exists, password unchanged | `ALTER ROLE` to same value (no-op) |
| Role exists, superuser password rotated | `ALTER ROLE` updates to new password |

---

## 7. Backend Deployment

**Env vars:**
- `INSTANCEZ_DATABASE_URL` — from Secret (`databaseUrl` key)
- `INSTANCEZ_ADMIN_KEY` — from Secret (`adminKey` key)
- `JWT_SECRET` — from Secret (`jwtSecret` key)
- `INSTANCEZ_PORT: "8080"` — literal

**Volumes:**
- `instancez.yaml` ConfigMap mounted at `/app/instancez.yaml`

**Init container:**
Runs `pg_isready` against the postgres service before the main container starts (only when `postgres.enabled=true`). Prevents the CLI's role provisioning step from racing against an unready postgres.

---

## 8. Networking

**`service.yaml`:** ClusterIP mapping `service.port` (default 80) → container port 8080.

**`ingress.yaml`:** Gated on `ingress.enabled: false`. When enabled:
- `ingressClassName` from `ingress.className`
- Single host rule from `ingress.host`, all paths → backend Service
- TLS from `ingress.tls` (user-managed cert Secrets)
- `ingress.annotations` passed through verbatim — no predefined annotations

**`NOTES.txt`:** Post-install message showing the effective database URL shape (password redacted), ingress status, and a note if `adminKey`/`jwtSecret` were auto-generated (recommend storing them).

---

## 9. Breaking Changes

- `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` are no longer accepted as external inputs. Users providing these must migrate to `INSTANCEZ_DATABASE_URL` (superuser URL only).
- `docker-compose.dev.yaml` must be updated: remove the `scripts/postgres-init` volume mount from the postgres service, remove `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` from the backend service, and add `INSTANCEZ_DATABASE_URL: postgres://instancez:instancez@postgres:5432/instancez?sslmode=disable` (the existing postgres superuser credential).
- `scripts/postgres-init/` is no longer needed and can be deleted — role provisioning now happens via the CLI startup sequence in all deployment modes.

---

## 10. Docs Updates Required

All changes to CLI behaviour, env vars, and deployment configuration must be reflected in `docs/site/src/content/docs/`.

**`deploy/env-vars.md`:**
- Remove `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` entries
- Update `INSTANCEZ_DATABASE_URL` — no longer dev-only; now used by both `inz dev` and `inz serve` as the single required database credential. Remove the mention of `.development.env` provisioning behaviour.

**`deploy/docker.md`:**
- Remove the init SQL section (`init/01-roles.sql`) — role provisioning now happens automatically via the CLI on startup
- Replace `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` with `INSTANCEZ_DATABASE_URL` (superuser DSN) in all examples
- Update the docker-compose example to reflect the new env var

**`deploy/self-hosted.md`:**
- Same env var changes as docker.md — remove owner/auth URLs, add superuser URL
- Remove any manual role creation instructions

**`deploy/kubernetes.md`** (new page):
- Helm chart installation guide: `helm install`, required values (`adminKey`, `jwtSecret`, `postgres.password` or auto-generated), `config:` block
- Bundled vs external Postgres (`postgres.enabled`)
- Ingress configuration example
- Secret management note (auto-generated credentials, where to find them post-install)
- Upgrade and password rotation instructions

---

## 11. Out of Scope

- Helm repository / OCI publishing (separate concern)
- Horizontal pod autoscaling
- NetworkPolicy resources
- Multi-replica Postgres (HA) — the bundled StatefulSet is single-replica; production HA Postgres should use `postgres.enabled=false` with an external managed database
