---
title: Docker
description: Run instancez with Docker or Docker Compose.
---

## Quick start with Docker Compose

Before you can start instancez, Postgres needs the two login roles it expects (`instancez_owner` and `authenticator`). The init SQL below provisions them on first container boot.

Create the following files in a new directory:

**`init/01-roles.sql`**

```sql
CREATE ROLE instancez_owner LOGIN PASSWORD 'change-me'
    CREATEROLE CREATEDB BYPASSRLS REPLICATION;

CREATE ROLE authenticator LOGIN PASSWORD 'change-me' NOINHERIT;

CREATE ROLE anon NOLOGIN;
CREATE ROLE authenticated NOLOGIN;
CREATE ROLE service_role NOLOGIN BYPASSRLS;

GRANT anon, authenticated, service_role TO authenticator;

ALTER DATABASE instancez OWNER TO instancez_owner;
ALTER SCHEMA public OWNER TO instancez_owner;
GRANT ALL ON SCHEMA public TO instancez_owner;
```

**`compose.yaml`**

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: instancez
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./init:/docker-entrypoint-initdb.d:ro
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "postgres"]
      interval: 2s
      timeout: 3s
      retries: 10

  instancez:
    image: ghcr.io/instancez/instancez:latest
    ports:
      - "8080:8080"
    environment:
      INSTANCEZ_OWNER_DATABASE_URL: postgres://instancez_owner:${OWNER_DB_PASSWORD}@postgres:5432/instancez?sslmode=disable
      INSTANCEZ_AUTH_DATABASE_URL: postgres://authenticator:${AUTH_DB_PASSWORD}@postgres:5432/instancez?sslmode=disable
      INSTANCEZ_ADMIN_KEY: ${ADMIN_KEY}
    volumes:
      - ./instancez.yaml:/app/instancez.yaml
      - uploads:/app/uploads
    command: ["inz", "serve", "--migrate"]
    depends_on:
      postgres:
        condition: service_healthy

volumes:
  pgdata:
  uploads:
```

**`.env`** (never commit this file)

```
POSTGRES_PASSWORD=change-me
OWNER_DB_PASSWORD=change-me
AUTH_DB_PASSWORD=change-me
ADMIN_KEY=your-secret-admin-key
```

Two database URLs are required — see [Environment Variables](/instancez/deploy/env-vars/) for why they are separate.

Start everything:

```bash
docker compose up
```

The API is ready at `http://localhost:8080` once the `instancez` container logs `listening`.

## Standalone Docker

```bash
docker run -d \
  -p 8080:8080 \
  -e INSTANCEZ_OWNER_DATABASE_URL="postgres://instancez_owner:password@host:5432/instancez" \
  -e INSTANCEZ_AUTH_DATABASE_URL="postgres://authenticator:password@host:5432/instancez" \
  -e INSTANCEZ_ADMIN_KEY="your-admin-key" \
  -v $(pwd)/instancez.yaml:/app/instancez.yaml \
  ghcr.io/instancez/instancez:latest \
  inz serve --migrate
```

## Image tags

| Tag | Description |
|-----|-------------|
| `ghcr.io/instancez/instancez:latest` | Latest release, multi-arch (linux/amd64 + linux/arm64) |
| `ghcr.io/instancez/instancez:v1.2.3` | Pinned release |

The `latest` tag is a multi-arch manifest list and works on both amd64 and arm64 hosts. For AWS Lambda, see the [Lambda deployment guide](/instancez/deploy/lambda/) — Lambda requires a single-arch image from a private ECR registry and cannot use these tags directly.

## Health checks

| Endpoint | Behaviour |
|----------|-----------|
| `GET /live` | Returns `200` when the process is alive |
| `GET /health` | Returns `200` when the app is initialized |
| `GET /ready` | Returns `200` when Postgres is reachable; `503` otherwise |

Use `/ready` for load-balancer health checks and `/live` for liveness probes.
