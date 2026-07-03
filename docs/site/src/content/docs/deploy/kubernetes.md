---
title: Kubernetes
description: Deploy instancez on Kubernetes using the official Helm chart.
---

## Overview

instancez ships a Helm chart at `helm/instancez/` in the repository. It deploys the instancez backend and, optionally, a bundled PostgreSQL instance. Sensitive values (`adminKey` and the Postgres password) are auto-generated on first install and preserved across upgrades.

## Prerequisites

- [kubectl](https://kubernetes.io/docs/tasks/tools/) configured against your cluster
- [Helm 3.x](https://helm.sh/docs/intro/install/)
- A running Kubernetes cluster

## Quick start (bundled Postgres)

The default chart values enable a bundled Postgres instance. To install with all defaults:

```bash
helm install instancez ./helm/instancez
```

`adminKey` and the Postgres password are auto-generated as 32-character random strings and stored in a Kubernetes Secret named `instancez`. They are stable — the values are generated once and reused on every subsequent `helm upgrade`.

Retrieve the generated credentials:

```bash
kubectl get secret instancez -o jsonpath='{.data.adminKey}' | base64 -d
```

## Providing your own values

Pass values on the command line with `--set`:

```bash
helm install instancez ./helm/instancez \
  --set adminKey=my-admin-key
```

Or save them in a values file and pass it with `-f`:

```yaml
# my-values.yaml
adminKey: my-admin-key
```

```bash
helm install instancez ./helm/instancez -f my-values.yaml
```

### Custom instancez config

The `config` value is written verbatim to a ConfigMap and mounted as `/app/instancez.yaml` inside the pod. Override it in your values file to configure tables, RLS policies, and other instancez settings:

```yaml
# my-values.yaml
config: |
  version: 1
  project:
    name: my-project
  server:
    port: 8080
  tables:
    - name: posts
      columns:
        - name: id
          type: uuid
          primaryKey: true
```

See [instancez.yaml reference](/instancez/api-reference/config/) for the full schema.

## External Postgres

To use an existing Postgres instance instead of the bundled one, disable the bundled Postgres and provide a superuser DSN:

```bash
helm install instancez ./helm/instancez \
  --set postgres.enabled=false \
  --set externalPostgres.url="postgres://superuser:pass@your-db:5432/instancez"
```

The DSN must be a superuser connection — instancez provisions the `instancez_owner` and `authenticator` roles on startup and requires `CREATEROLE CREATEDB BYPASSRLS` privileges.

## Ingress

Enable and configure Ingress in your values file:

```yaml
ingress:
  enabled: true
  className: nginx
  host: instancez.example.com
```

TLS can be added via the `ingress.tls` list:

```yaml
ingress:
  enabled: true
  className: nginx
  host: instancez.example.com
  tls:
    - hosts:
        - instancez.example.com
      secretName: instancez-tls
```

## Upgrading

```bash
helm upgrade instancez ./helm/instancez
```

Auto-generated secrets (`adminKey` and the bundled Postgres password) are read from the existing Kubernetes Secret and not regenerated on upgrade, so credentials are preserved.

To upgrade with a new values file:

```bash
helm upgrade instancez ./helm/instancez -f my-values.yaml
```

## Password rotation

To rotate credentials:

1. Update the `instancez` Kubernetes Secret directly — edit `adminKey` or `databaseUrl` (and the bundled Postgres password if applicable).
2. Restart the pod so instancez picks up the new values:

```bash
kubectl rollout restart deployment/instancez
```

instancez re-reads the superuser DSN on startup and syncs all Postgres role passwords, so the `instancez_owner` and `authenticator` role passwords are updated automatically from the new DSN.

## Health checks

| Endpoint | Behaviour |
|----------|-----------|
| `GET /live` | Returns `200` when the process is alive |
| `GET /health` | Returns `200` when the app is initialized |
| `GET /ready` | Returns `200` when Postgres is reachable; `503` otherwise |

The chart configures liveness (`/live`) and readiness (`/ready`) probes automatically. Use `/ready` for external load-balancer checks.
