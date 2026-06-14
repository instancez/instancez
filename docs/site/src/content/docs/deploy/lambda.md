---
title: AWS Lambda
description: Deploy instancez as an AWS Lambda container function.
---

instancez runs on Lambda as a container function. The Lambda Web Adapter translates Lambda invocations into HTTP requests to `inz serve` on port 8080 — no handler shim required.

## Pull and push to ECR

Lambda requires a single-arch image in a private ECR repository. Pull the arm64 layer from the official instancez image and push it directly to your ECR repo — no local build needed:

```bash
VERSION=v0.1.0   # replace with the release you want to pin

# Authenticate to ECR
aws ecr get-login-password --region us-east-1 | \
  docker login --username AWS --password-stdin \
  123456789012.dkr.ecr.us-east-1.amazonaws.com

# Pull the arm64 layer from ghcr, tag, and push to your ECR repo
docker pull --platform linux/arm64 \
  ghcr.io/instancez/instancez:${VERSION}-lambda

docker tag \
  ghcr.io/instancez/instancez:${VERSION}-lambda \
  123456789012.dkr.ecr.us-east-1.amazonaws.com/instancez/prod:${VERSION}-lambda-arm64

docker push \
  123456789012.dkr.ecr.us-east-1.amazonaws.com/instancez/prod:${VERSION}-lambda-arm64
```

The image includes `inz serve`, Node.js (for code functions), and the Lambda Web Adapter. The default CMD is `inz serve --data --migrate`, which runs migrations on every cold start.

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
| `INSTANCEZ_ADMIN_KEY` | Yes | Secret for admin API access |
| `INSTANCEZ_CONFIG` | No | Config source; defaults to `instancez.yaml` in the working directory. Set to `s3://bucket/key` to load from S3 (see below). |

See [Environment Variables](/deploy/env-vars/) for the full reference.

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

To enable config watching (re-fetch on poll interval), set `INSTANCEZ_WATCH=true` and `INSTANCEZ_WATCH_INTERVAL=60s` on the function.
