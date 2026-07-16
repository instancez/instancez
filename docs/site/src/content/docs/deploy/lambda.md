---
title: AWS Lambda
description: Deploy instancez as an AWS Lambda container function.
---

instancez runs on Lambda as a container function. The Lambda Web Adapter translates Lambda invocations into HTTP requests to `inz serve` on port 8080 — no handler shim required.

## Build and push to ECR

Lambda requires a **single-arch** image in a private ECR repository. The public `ghcr.io/instancez/instancez:<version>-lambda` tag is a multi-arch manifest list — Lambda rejects manifest lists, so don't pull, tag, and push that image directly. Instead build the per-arch image from source using `Dockerfile.lambda`:

```bash
VERSION=v0.1.0   # replace with the release/commit you want to pin

# Authenticate to ECR
aws ecr get-login-password --region us-east-1 | \
  docker login --username AWS --password-stdin \
  123456789012.dkr.ecr.us-east-1.amazonaws.com

# Build a single-arch (arm64) image from source and push it straight to ECR
git clone --branch ${VERSION} --depth 1 https://github.com/instancez/instancez.git
cd instancez

docker buildx build \
  --platform linux/arm64 \
  --provenance=false \
  -f Dockerfile.lambda \
  -t 123456789012.dkr.ecr.us-east-1.amazonaws.com/instancez/prod:${VERSION}-lambda-arm64 \
  --push .
```

The image includes `inz serve`, Node.js (for code functions), and the Lambda Web Adapter. The default CMD is `inz serve --migrate`, which runs migrations on every cold start.

## Storage on Lambda

Lambda functions are stateless and ephemeral — use the S3 storage provider, not local. With S3 configured, instancez exposes a direct upload API at `/api/storage/<bucket>/sign` that returns a presigned S3 URL. The file bytes go straight to S3 without passing through the Lambda function:

```js
const { id, upload_url } = await fetch('/api/storage/avatars/sign', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${jwt}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ content_type: file.type, size: file.size }),
}).then(r => r.json())

// Upload directly to S3 — Lambda is not in this path
await fetch(upload_url, { method: 'PUT', headers: { 'Content-Type': file.type }, body: file })
```

This avoids Lambda's 6 MB payload limit and keeps large uploads off the function entirely. See [Storage — Direct upload](/instancez/build/storage/) for the full endpoint spec.

## Lambda configuration

When creating or updating the function:

| Setting | Value |
|---------|-------|
| Architecture | `arm64` |
| Memory | 512 MB minimum; 1024 MB recommended for functions with code functions |
| Timeout | 30 s minimum; match your slowest expected request |
| Package type | Image |

## Environment variables

Set these on the Lambda function:

| Variable | Required | Description |
|----------|----------|-------------|
| `INSTANCEZ_OWNER_DATABASE_URL` | Yes | Privileged DSN used for migrations (`instancez_owner` role) |
| `INSTANCEZ_AUTH_DATABASE_URL` | Yes | Request-pool DSN (`authenticator` role) |
| `INSTANCEZ_PUBLISHABLE_KEY` | Yes | Publishable API key (`inz_publishable_…`); client-safe, maps to `anon` |
| `INSTANCEZ_SECRET_KEY` | Yes | Secret API key (`inz_secret_…`); server-side, maps to `service_role` and unlocks the admin API |
| `INSTANCEZ_CONFIG` | No | Config source; defaults to `instancez.yaml` in the working directory. Set to `s3://bucket/key` to load from S3 (see below). Use this only if you have no code functions — it ships the YAML but not function source. |
| `INSTANCEZ_BUNDLE` | No | Use instead of `INSTANCEZ_CONFIG` when you have code functions. Points at an archive built by `inz bundle --output s3://bucket/key` that packages `instancez.yaml` and `functions/` together, so function source actually reaches the Lambda instance. |

See [Environment Variables](/instancez/deploy/env-vars/) for the full reference.

## Config from S3

On Lambda the working directory is read-only, so the most practical configuration source is S3:

```
INSTANCEZ_CONFIG=s3://my-bucket/my-app/instancez.yaml
```

When `INSTANCEZ_CONFIG` is an S3 URI, instancez fetches the config at startup. The S3 client uses the function's IAM role by default. To use explicit credentials, set these environment variables (distinct from the storage-provider variables):

| Variable | Description |
|----------|-------------|
| `S3_REGION` | AWS region of the config bucket |
| `S3_ENDPOINT` | Custom endpoint (for S3-compatible stores) |
| `S3_ACCESS_KEY_ID` | Access key ID |
| `S3_SECRET_ACCESS_KEY` | Secret access key |

The IAM role approach is simpler — grant the Lambda execution role `s3:GetObject` on the config object and omit the credential variables.

If you have code functions, build and upload a bundle instead (`inz bundle --output s3://my-bucket/my-app/bundle.tar.gz`) and set `INSTANCEZ_BUNDLE` to that URI in place of `INSTANCEZ_CONFIG` — a bare YAML config doesn't carry function source with it.

To enable config watching (re-fetch on poll interval), set `INSTANCEZ_WATCH=true` and `INSTANCEZ_WATCH_INTERVAL=60s` on the function.
