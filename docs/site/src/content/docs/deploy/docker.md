---
title: Docker
description: Run instancez with Docker or Docker Compose.
---

## Quick start with Docker Compose

instancez provisions all required Postgres roles automatically on startup — no init SQL scripts needed.

Create the following files in a new directory:

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
      INSTANCEZ_DATABASE_URL: postgres://postgres:${POSTGRES_PASSWORD}@postgres:5432/instancez?sslmode=disable
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
ADMIN_KEY=your-secret-admin-key
```

See [Environment Variables](/instancez/deploy/env-vars/) for the full variable reference.

Start everything:

```bash
docker compose up
```

The API is ready at `http://localhost:8080` once the `instancez` container logs `listening`.

## Standalone Docker

```bash
docker run -d \
  -p 8080:8080 \
  -e INSTANCEZ_DATABASE_URL="postgres://postgres:password@host:5432/instancez" \
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
