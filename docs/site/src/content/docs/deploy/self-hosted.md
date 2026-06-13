---
title: Self-hosted
description: Run instancez on a bare metal server or VPS with inz serve.
---

## Download the binary

```bash
curl -fsSL https://get.instancez.io | sh
```

This installs `inz` to `~/.local/bin`. Verify the install:

```bash
inz --version
```

Alternatively, download a release binary directly from [GitHub Releases](https://github.com/instancez/instancez/releases) and place it on your `PATH`.

## Configure

Create a `.production.env` file next to `instancez.yaml`. `inz serve` loads this file automatically when the config source is a local file. Shell environment variables always take precedence over values in `.production.env`.

```bash
# .production.env — do not commit this file

INSTANCEZ_OWNER_DATABASE_URL=postgres://instancez_owner:password@localhost:5432/mydb
INSTANCEZ_AUTH_DATABASE_URL=postgres://authenticator:password@localhost:5432/mydb
INSTANCEZ_ADMIN_KEY=your-secret-admin-key
```

Postgres must already have the `instancez_owner` and `authenticator` roles provisioned before `inz serve` starts. See the [Docker guide](/deploy/docker/) for the init SQL, or run it manually against your database.

See [Environment Variables](/deploy/env-vars/) for the full list of available variables.

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

## systemd unit

Create `/etc/systemd/system/instancez.service`:

```ini
[Unit]
Description=instancez API server
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User=instancez
WorkingDirectory=/opt/instancez
EnvironmentFile=/opt/instancez/.production.env
ExecStart=/usr/local/bin/inz serve --migrate
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Enable and start it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable instancez
sudo systemctl start instancez
```

Check the logs:

```bash
journalctl -u instancez -f
```

## Nginx reverse proxy

A minimal Nginx configuration:

```nginx
server {
    listen 80;
    server_name api.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
}
```

instancez reads `X-Forwarded-Proto` and `X-Forwarded-Host` to construct the correct public base URL in the OpenAPI spec and auth redirect flows. Set these headers in Nginx if instancez sits behind a TLS terminator.

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
