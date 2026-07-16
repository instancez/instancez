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
    image: ghcr.io/instancez/instancez:1.2.3 # pin to a released version, see Image tags below
    ports:
      - "8080:8080"
    environment:
      INSTANCEZ_DATABASE_URL: postgres://postgres:${POSTGRES_PASSWORD}@postgres:5432/instancez?sslmode=disable
      INSTANCEZ_PUBLISHABLE_KEY: ${PUBLISHABLE_KEY}
      INSTANCEZ_SECRET_KEY: ${SECRET_KEY}
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
PUBLISHABLE_KEY=inz_publishable_your-key
SECRET_KEY=inz_secret_your-key
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
  -e INSTANCEZ_PUBLISHABLE_KEY="inz_publishable_your-key" \
  -e INSTANCEZ_SECRET_KEY="inz_secret_your-key" \
  -v $(pwd)/instancez.yaml:/app/instancez.yaml \
  ghcr.io/instancez/instancez:1.2.3 \
  inz serve --migrate
```

## Image tags

| Tag | Description |
|-----|-------------|
| `ghcr.io/instancez/instancez:1.2.3` | Released version (note: no leading `v`), multi-arch (linux/amd64 + linux/arm64) |
| `ghcr.io/instancez/instancez:1.2.3-standalone` | Same image, explicit alias for the default flavor |
| `ghcr.io/instancez/instancez:1.2.3-lambda` | Lambda-flavored image, also multi-arch |
| `ghcr.io/instancez/instancez:dev-<sha7>` | Built from every push to `main`, not a release |

There is no `latest` tag — always pin to a version. For AWS Lambda, see the [Lambda deployment guide](/instancez/deploy/lambda/) — Lambda requires a single-arch image from a private ECR registry, so the multi-arch `-lambda` tag above cannot be used directly.

## Health checks

| Endpoint | Behaviour |
|----------|-----------|
| `GET /live` | Returns `200` when the process is alive |
| `GET /health` | Returns `200` when the app is initialized |
| `GET /ready` | Returns `200` when Postgres is reachable; `503` otherwise |

Use `/ready` for load-balancer health checks and `/live` for liveness probes.
