---
title: Auth API
description: /auth/v1/* endpoint reference, request/response shapes, and JWT claims structure.
---

## Endpoints

### User-facing

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/v1/signup` | Sign up with email/password, or anonymous signup (omit email) |
| `POST` | `/auth/v1/token` | Exchange credentials or refresh token for a session (`grant_type=password\|refresh_token`) |
| `GET` | `/auth/v1/user` | Get the current user (requires JWT) |
| `PUT` | `/auth/v1/user` | Update email, password, or user metadata (requires JWT) |
| `POST` | `/auth/v1/logout` | Invalidate the current session (requires JWT) |
| `GET` | `/auth/v1/settings` | Public auth configuration (providers enabled, etc.) |
| `POST` | `/auth/v1/recover` | Send a password recovery email |
| `POST` | `/auth/v1/verify` | Verify a token (signup, email change, recovery) |
| `GET` | `/auth/v1/verify` | Handle email verification link clicks |
| `POST` | `/auth/v1/otp` | Send a one-time password (magic link / OTP) |
| `POST` | `/auth/v1/resend` | Resend a verification or OTP email |
| `GET` | `/auth/v1/reauthenticate` | Trigger re-authentication challenge (requires JWT) |
| `POST` | `/auth/v1/reauthenticate` | Complete re-authentication (requires JWT) |
| `POST` | `/auth/v1/token/verify` | Verify a token without consuming it |
| `GET` | `/auth/v1/.well-known/jwks.json` | JSON Web Key Set for JWT verification |
| `GET` | `/auth/v1/authorize` | Start an OAuth flow |
| `GET` | `/auth/v1/callback/google` | OAuth callback for Google |
| `GET` | `/auth/v1/callback/github` | OAuth callback for GitHub |

### Identity linking (requires JWT)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/auth/v1/user/identities` | List linked OAuth identities |
| `POST` | `/auth/v1/user/identities/authorize` | Link a new OAuth identity |
| `DELETE` | `/auth/v1/user/identities/:id` | Unlink an OAuth identity |

### MFA (requires JWT)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/auth/v1/factors` | List enrolled factors |
| `POST` | `/auth/v1/factors` | Enroll a new factor (TOTP) |
| `DELETE` | `/auth/v1/factors/:factor_id` | Unenroll a factor |
| `POST` | `/auth/v1/factors/:factor_id/challenge` | Create a challenge |
| `POST` | `/auth/v1/factors/:factor_id/verify` | Verify a challenge (issues aal2 token) |

### Admin (service role key required)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/v1/invite` | Send an invite email |
| `POST` | `/auth/v1/admin/generate_link` | Generate a magic link or verification URL |
| `POST` | `/auth/v1/admin/users` | Create a user |
| `GET` | `/auth/v1/admin/users` | List users |
| `GET` | `/auth/v1/admin/users/:uid` | Get a user |
| `PUT` | `/auth/v1/admin/users/:uid` | Update a user |
| `DELETE` | `/auth/v1/admin/users/:uid` | Delete a user |
| `POST` | `/auth/v1/admin/users/:uid/signout` | Sign out a user (all sessions) |
| `DELETE` | `/auth/v1/admin/users/:uid/factors/:factor_id` | Delete a user's MFA factor |

## Request headers

| Header | Description |
|--------|-------------|
| `Authorization: Bearer <jwt>` | Authenticated requests; required where noted |
| `apikey: <anon-key>` | Public/anonymous requests |
| `Content-Type: application/json` | Required for POST/PUT bodies |

## JWT claims structure

Claims present in every access token issued by instancez:

| Claim | Type | Description |
|-------|------|-------------|
| `sub` | `string` (UUID) | User ID |
| `role` | `string` | Always `authenticated` for signed-in users |
| `email` | `string` | User's email address |
| `iss` | `string` | Issuer URL |
| `aud` | `string` | Always `authenticated` |
| `iat` | `number` | Issued-at (Unix timestamp) |
| `exp` | `number` | Expiry (Unix timestamp) |
| `session_id` | `string` | Unique session identifier |
| `is_anonymous` | `boolean` | `true` for anonymous users |
| `app_metadata` | `object` | Provider info (`provider`, `providers`); `aal` after MFA verify |
| `user_metadata` | `object` | User-supplied metadata from signup or profile update |

Anonymous sessions issued by `POST /auth/v1/signup` with no email carry `is_anonymous: true`.

After MFA verification, `app_metadata.aal` is set to `"aal2"`.

## Session response shape

Returned by sign-in, sign-up, token exchange, and OAuth callbacks:

```json
{
  "access_token": "<jwt>",
  "token_type": "bearer",
  "expires_in": 900,
  "expires_at": 1718000000,
  "refresh_token": "<opaque-token>",
  "user": {
    "id": "<uuid>",
    "aud": "authenticated",
    "role": "authenticated",
    "email": "user@example.com",
    "email_confirmed_at": "2024-01-01T00:00:00Z",
    "confirmed_at": "2024-01-01T00:00:00Z",
    "last_sign_in_at": "2024-06-01T12:00:00Z",
    "app_metadata": { "provider": "email", "providers": ["email"] },
    "user_metadata": {},
    "identities": [],
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-06-01T12:00:00Z"
  }
}
```

`refresh_token` is only present when `refresh_tokens: true` is set in `instancez.yaml`. `expires_in` is in seconds.
