---
title: MCP connector (Claude)
description: Connect Claude or Claude Code to instancez Cloud over MCP to create projects, run SQL, read logs, and host static sites from chat.
---

The instancez Cloud MCP server lets an AI assistant manage your instancez Cloud account for you. Connect it to Claude or Claude Code, then ask in plain language to create a backend, query its data, read its logs, or publish a static site.

Server URL: `https://my.instancez.ai/mcp` (remote, Streamable HTTP).

## What it does

Each instancez project is one Postgres database plus a Supabase-compatible API, defined by a single `instancez.yaml`. The connector exposes that as 28 tools. The assistant picks the tools; you describe the goal.

You need an instancez Cloud account. The connector acts as you, with your organization roles.

## How it works

- You sign in with your instancez account using OAuth 2.0 (dynamic client registration and PKCE). No API key to copy.
- Every tool acts on one organization. With one organization it is used automatically. With several, the assistant passes `org_id` (found with `whoami`), or you pin one by adding `?org_id=<id>` to the URL, or by sending the `X-Instancez-Org` header.
- OAuth tokens work only on the `/mcp` endpoint. They expire after 90 days. There are no refresh tokens, so reconnect to sign in again.
- `create_app` and `update_app` save the config and deploy it. Deploys are asynchronous and can take up to about 90 seconds.

## Set up

### Claude

1. Open Settings, then Connectors.
2. Choose Add custom connector.
3. Paste `https://my.instancez.ai/mcp`.
4. Choose Connect and sign in with your instancez account.

### Claude Code

```bash
claude mcp add --transport http instancez https://my.instancez.ai/mcp
```

Then run `/mcp` inside Claude Code and authenticate.

To pin an organization, use `https://my.instancez.ai/mcp?org_id=<id>` as the URL.

## Tools

Read-only tools do not change anything:

| Tool | What it does |
| --- | --- |
| `whoami` | Your email, user id, and organizations |
| `list_apps` | List projects |
| `get_app` | Project details, config, URL, and deploy status |
| `get_app_logs` | Recent backend logs |
| `config_docs` | The `instancez.yaml` syntax reference |
| `run_sql_read` | Read-only SQL query |
| `list_users` | List a project's auth users |
| `list_sites` | List static sites |
| `get_site` | Static site details |
| `read_function_file` | Read a code function file |
| `get_function_deps` | Read a code function's dependencies |

Write tools change data or delete things:

| Tool | What it does |
| --- | --- |
| `create_app` | Create and deploy a project from a config |
| `update_app` | Change a project's config and redeploy |
| `delete_app` | Delete a project (no undo) |
| `set_app_secrets` | Set or rotate project secrets |
| `run_sql_write` | Run INSERT, UPDATE, or DELETE and commit |
| `create_user`, `update_user`, `delete_user` | Manage auth users |
| `write_function_file`, `set_function_deps` | Edit code functions |
| `create_site`, `update_site`, `delete_site` | Manage static sites |
| `create_site_upload` | Get an upload URL for a site build |
| `set_site_custom_domain`, `verify_site_custom_domain`, `remove_site_custom_domain` | Manage a site's custom domain |

Both SQL tools run as `service_role`, so they bypass RLS and see every row. DDL is rejected: change the schema through `update_app`. `run_sql_read` runs in a read-only transaction. Results are capped at 1000 rows with a 10 second timeout.

## Example prompts

**1. "Create a todo backend with a tasks table where each user only sees their own rows."**
The assistant calls `config_docs` to read the syntax, then `create_app` with a config that has a `tasks` table and an RLS policy on the owner column. It checks `get_app` until the deploy finishes.

**2. "How many users signed up this week in my todo project?"**
The assistant calls `list_apps` to find the project, then `run_sql_read` with a count over `auth.users` filtered by creation date. Nothing is changed.

**3. "Show the last hour of logs for my todo project and explain any errors."**
The assistant calls `get_app_logs` with `minutes: 60` and summarizes the events, pointing out errors and likely causes.

**4. "Create a static site called docs-preview and give me an upload URL for my dist folder."**
The assistant calls `create_site`, then `create_site_upload`. You get a URL that accepts a PUT of your built folder, for example:

```bash
tar czf dist.tgz -C dist . && curl -T dist.tgz "<upload_url>"
```

## Troubleshooting

**Sign-in loops, or Claude says it cannot reach the connector.** Check the URL ends in `/mcp`, then remove and re-add the connector. On a corporate network, the client must be able to reach `my.instancez.ai`.

**Tools stop working after a while.** The token expired (90 days). Disconnect and connect again.

**"You belong to several organizations".** Ask the assistant to run `whoami`, then name the organization. Or pin one with `?org_id=<id>` on the URL.

**SQL, user, or log tools say the project is still deploying or provisioning.** A deploy takes up to about 90 seconds after `create_app` or `update_app`. Retry shortly. If `get_app` shows `deploy_failed`, read its `deployment.error`.

**`get_app_logs` returns a throttling error.** It is limited to about 2 calls per second per organization. Wait a moment and retry.

**`feature_not_available` on a custom domain or site.** Your plan does not include that feature.

**A site upload is rejected.** Upload a zip or tar.gz with `index.html` at the archive root. The limits are 5000 files and 100 MiB.

**Deleted a project but the site is still up.** Deleting a project does not delete static sites. Use `delete_site`.

**A SQL statement fails with a DDL error.** Schema changes go through `update_app`, not SQL.

## Support and privacy

- Support: support@instancez.ai
- Security issues: security@instancez.ai
- Privacy policy: [instancez.ai/privacy](https://instancez.ai/privacy)

For writing instancez configs in a local repo instead, see [Coding Agents](/coding-agents/). For the CLI route to the same service, see [instancez Cloud](/deploy/cloud/).
