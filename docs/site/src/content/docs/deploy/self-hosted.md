---
title: Self-hosted
description: Run instancez on a bare metal server or VPS with inz serve.
---

## Download the binary

```bash
curl -fsSL https://get.instancez.ai | sh
```

This installs `inz` to `~/.local/bin`. Verify the install:

```bash
inz version
```

Alternatively, download a release binary directly from [GitHub Releases](https://github.com/instancez/instancez/releases) and place it on your `PATH`.

## Configure

Create a `.production.env` file next to `instancez.yaml`. `inz serve` loads this file automatically when the config source is a local file. Shell environment variables always take precedence over values in `.production.env`.

```bash
# .production.env — do not commit this file

INSTANCEZ_DATABASE_URL=postgres://postgres:password@localhost:5432/mydb
INSTANCEZ_ADMIN_KEY=your-secret-admin-key
```

Roles (`instancez_owner`, `authenticator`, `anon`, `authenticated`, `service_role`) are provisioned automatically by instancez on first startup.

See [Environment Variables](/instancez/deploy/env-vars/) for the full list of available variables.

## Validate config

Before deploying a config change, check it for errors:

```bash
inz validate
```

This runs a structural check on `instancez.yaml` without connecting to the database. Fix any reported errors before restarting the server.

## Run

```bash
inz serve --migrate
```

`--migrate` applies pending schema migrations on startup. Drop it if you manage migrations separately. The server listens on port 8080 by default; set `INSTANCEZ_PORT` or `--port` to change it.

On startup, `inz serve` logs a JSON stream to stdout. The server is ready when you see `"listening"`.

## Health checks

| Endpoint | Behaviour |
|----------|-----------|
| `GET /live` | Returns `200` when the process is alive |
| `GET /health` | Returns `200` when the app is initialized |
| `GET /ready` | Returns `200` when Postgres is reachable; `503` otherwise |

Use `/ready` for load-balancer health checks. Configure Nginx to check it before routing traffic:

```nginx
location /ready {
    proxy_pass http://127.0.0.1:8080/ready;
    access_log off;
}
```
