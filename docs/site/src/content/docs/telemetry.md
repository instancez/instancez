---
title: Telemetry
description: What the inz CLI sends, what it never sends, crash reports, and how to turn it all off.
---

The `inz` CLI sends anonymous usage and error data to help the instancez team see where people get stuck. It is on by default. You can turn it off with one command. This page lists everything it sends.

Telemetry comes from the CLI process only. The running server never reports request or user data. `dev_ready` does send table and function counts.

The first time you run a command with telemetry on, `inz` prints this once to stderr:

```
inz collects anonymous usage and error data to improve the CLI.
Disable: inz telemetry disable  (or INSTANCEZ_TELEMETRY_DISABLED=1)
Details: https://docs.instancez.ai/telemetry/
```

The install script prints the same three lines after installing. The notice is not shown for `serve`, `version`, `telemetry`, `help`, or `completion`. The `telemetry`, `help`, and `completion` commands send nothing, not even `command_run`, and never create `~/.instancez/telemetry.json`.

## The hosted installer

`curl -fsSL https://get.instancez.ai | sh` and the Windows one-liner are served by a small Cloudflare Worker. Each time it serves a script it sends one `install_script` event to the same PostHog project: the script type (`sh` or `ps1`), the `ref`, `utm_source` and `utm_campaign` query values, the request URL, the `Referer` header, and the first 100 characters of your `User-Agent`. It uses a random id per request, creates no person profile, and disables GeoIP lookup. The CLI opt-outs do not apply to it, since it runs before `inz` is installed. To skip it, download a binary from [GitHub Releases](https://github.com/instancez/instancez/releases).

## Turn it off

Any one of these is enough:

| Method | Scope |
|--------|-------|
| `inz telemetry disable` | Saved in `~/.instancez/telemetry.json`. Applies to every run. |
| `--no-telemetry` | One run. Works on any command. |
| `INSTANCEZ_TELEMETRY_DISABLED=1` | Environment. Also accepts `true`, `yes`, or `on`, in any case, with surrounding spaces ignored. |
| `DO_NOT_TRACK=1` | The [Do Not Track](https://consoledonottrack.com) convention. Accepts the same values. |

`inz telemetry enable` turns it back on. `inz telemetry status` shows whether it is on, how to turn it off (or why it is off), and your anonymous id.

Builds made from source (version `dev`) never send anything.

If `~/.instancez/telemetry.json` exists but cannot be read or parsed, telemetry stays off and the file is left untouched. `inz telemetry status` shows the reason, and `inz telemetry enable` or `disable` writes a fresh file.

When telemetry is off, `inz` makes no network call for it. An opt-out by environment variable, `--no-telemetry`, or a source build does not create `~/.instancez`.

Off also means no crash prompt, with one exception: a source build asks again only when `INSTANCEZ_TELEMETRY_DEBUG=1` is set, and then prints the payload instead of sending it.

## What is sent

Every event carries the same base properties:

| Property | Meaning |
|----------|---------|
| `version`, `commit` | CLI build |
| `os`, `arch` | For example `linux`, `arm64` |
| `is_ci` | `true` when the `CI` env var is set |
| `is_agent` | `true` when a known coding-agent env var is set (`CLAUDECODE`, `CURSOR_AGENT`, `GEMINI_CLI`, `CODEX_SANDBOX`) |
| `$process_person_profile`, `$geoip_disable` | Always `false` and `true`: no person profile, no geo lookup |

The PostHog Go library adds these on send, and `inz` cannot turn them off:

| Property | Meaning |
|----------|---------|
| `$os` | OS name, for example `Linux` or `Mac OS X` |
| `$os_version` | OS version: `sw_vers -productVersion` on macOS, `VERSION_ID` from `/etc/os-release` on Linux, the build number on Windows |
| `$os_distro` | Linux only: `NAME` from `/etc/os-release`, for example `Ubuntu` |
| `$go_version` | Go version the CLI was built with |
| `$lib`, `$lib_version` | `posthog-go` and its version |

Debug mode and the crash prompt's `v` view show these too, so what you see is what is sent.

The event's distinct id is a random UUID stored in `~/.instancez/telemetry.json`. It is not tied to you or your machine. Person profiles and geo lookup are switched off, and events carry no IP property. PostHog still sees the IP address of the connection. Whether it keeps it depends on the PostHog project setting "Discard client IP data", which the team keeps on.

### `command_run`

Sent once when a command finishes. Not sent by `inz serve`.

| Property | Meaning |
|----------|---------|
| `command` | Command path, for example `cloud deploy` |
| `success` | `true` or `false` |
| `duration_ms` | How long the command ran |
| `error_class` | On failure, a short class such as `config_invalid`, `env:db_auth` (database rejected the login), `env:db_connect` (any other database connection failure), `env:connect_refused` (any other refused connection), or `go:*fs.PathError` |
| `error_message` | On failure, the cleaned-up message (see below). For config validation failures, the first three errors as `path: message`. |
| `expected` | On failure, `true` when the cause is a user mistake, such as a config typo or a failed preflight check |

### `$exception`

Sent for every failed command, in addition to `command_run`. PostHog uses it to group common errors.

| Property | Meaning |
|----------|---------|
| `command` | Command path |
| `expected` | `true` for user mistakes, `false` for unexpected errors |
| `$exception_list` | Error class and cleaned-up message. Panics also carry structured Go stack frames: function, line, and a file path with your home directory shown as `~` and other absolute paths cut to the file name. |
| `error_detail` | The full error chain, cleaned up, when it adds to the message |

An unexpected `dev` or `serve` failure can also carry a crash report if you accept it. See [Crash reports](#crash-reports).

### `dev_ready`

Sent by `inz dev` right after it starts the HTTP server, before the port is confirmed bound. `command_run` for `dev` only arrives at exit, so this event shows whether `dev` booted.

| Property | Meaning |
|----------|---------|
| `duration_ms` | Time from process start to ready |
| `embedded_pg` | `true` when using `--embedded-pg` |
| `functions` | Number of code functions |
| `tables` | Number of tables |

### `dev_runtime_error`

Sent by `inz dev` when a hot reload fails while `dev` keeps running, including YAML typos. It never prompts. Each distinct error is sent once, and at most 10 per `dev` run.

| Property | Meaning |
|----------|---------|
| `error_class` | Short class |
| `error_message` | Cleaned-up message |
| `expected` | `true` for user mistakes |

## What is never sent

- Flag values or command arguments. Quoted text in error messages is replaced, so `unknown command "x"` is sent as `unknown command <value>`.
- The contents of `instancez.yaml`, except in a crash report you accept
- Environment variables or `.env` files
- Database data, connection strings, or passwords
- Your location. Geo lookup is disabled and events carry no IP property.

### Cleaned-up error messages

Error messages and stack traces are cleaned before they leave your machine. Messages are cut to 300 characters. Structural rules come first:

- Anything in double quotes, single quotes, or backticks becomes `<value>`. Apostrophes inside words, as in `can't`, do not start a quote. Three kinds of quoted text are kept, because they explain the error:
  - short lowercase identifiers such as `"node"`, `"user"`, or a mistyped command like `"deplyo"` (up to 25 characters of `a-z`, digits, `_`, `.`, `-`)
  - flag spellings such as `"--port"`
  - `inz` command and flag names such as `"inz cloud"` or `"-y, --yes"`

  Quoted text after a value word (`user`, `role`, `database`, `password`, `token`, `key`, `argument`, `parsing`, or a Postgres "invalid input" phrase) is always replaced. A short lowercase secret in quotes elsewhere can still appear.
- Absolute paths become `<path>/` plus the file name, for example `<path>/instancez.yaml`. That applies to Windows paths and to paths under `~` too.
- Hosts in `lookup <host>`, `dial tcp <host>`, `host:port (host)`, and TLS certificate errors become `<host>`.

Then pattern rules catch what is left:

- URLs with credentials become `scheme://<redacted>`
- Values after password-, secret-, token-, and key-like names (`password=`, `password: `, `"password":"…"`, `DB_PW = "…"`, `api_key=`) and secret query parameters (`client_secret=`, `X-Amz-Signature=`) become `<redacted>`
- Connection-string keywords `user=`, `database=`, `dbname=`, and `host=` have their values replaced
- `/Users/<name>` and `/home/<name>` become `/Users/<user>` and `/home/<user>`
- Email addresses, including ones without a dot such as `alice@localhost`, become `<email>`
- Known token shapes become `<redacted>`: JWTs, PEM private keys, `Bearer` and `Basic` values, Stripe (`sk_`, `rk_`, `pk_`, `whsec_`), GitHub (`ghp_`, `gho_`, `github_pat_`), Slack (`xoxb-`, webhook URLs), OpenAI and Anthropic (`sk-…`), Google (`AIza…`), instancez keys and tokens (`inz_secret_`, `inz_publishable_`, `instancez_pat_`), AWS access keys, `phc_`, `npm_`, and long base64 or hex runs

The rest of the message stays. Validation errors often name a table, column, or function from your config, so those names can appear in `error_message`.

## Crash reports

A crash report adds your project files to the `$exception` event so the team can reproduce a bug. It is never sent without your answer.

`inz` asks only when all of these hold:

- The command is `dev` or `serve`.
- The failure is unexpected, or a panic. User mistakes never prompt.
- stdin and stderr are both a terminal.
- For `serve`, the process is not in a container (`/.dockerenv`, `/run/.containerenv`, or `KUBERNETES_SERVICE_HOST`). A container started with `-t` has a terminal that nobody is typing into.
- `CI` is not set.
- Telemetry is not turned off. A source build counts as off unless `INSTANCEZ_TELEMETRY_DEBUG=1` is set.

Hot-reload failures in `dev` never prompt.

The prompt lists exactly what would be sent:

```
inz dev failed. Send a crash report to the instancez team?
It includes:
  - the error and stack trace
  - instancez.yaml (comments dropped; values other than schema structure and ${ENV} references replaced with <redacted>)
  - function source: functions/hello.js, functions/package.json (2 files, 4.1 KB; comments and long string literals replaced)
It never includes: .env files, environment variables, database data, node_modules.
Redaction is best-effort. Answer v to see the exact payload.
Send? [y/N/v=view]
```

Answer `y` to send. Anything else, including Enter, declines. Answer `v` to print the exact payload and be asked again. Keys you typed before the prompt appeared are discarded, so they cannot answer it. With no answer after 30 seconds, `inz` prints `no answer, not sent` and sends no report.

If you decline, `dev` still sends the plain `$exception` event described above, without any files. `serve` sends nothing.

### What a report includes

- `instancez.yaml`, with secrets redacted.
- Function entry files and `package*.json`. Files named `.env*` and files outside `functions/` are left out, including symlinks that point outside it.
- The whole event is capped at about 450 KB of JSON, below PostHog's 500 KB limit, so the error and stack always arrive. The largest files are dropped first and listed under `truncated_files`; if the stack is still too big, its outermost frames are dropped.
- When the config comes from S3 or a bundle, no files are attached and the prompt says so.

### How redaction works

`inz` reads `instancez.yaml` from disk, before any `${ENV}` expansion, and rebuilds it:

- All comments are dropped.
- Keys are kept. Table, field, function, and bucket names stay visible.
- Values are replaced with `<redacted>`, except booleans, numbers, `${ENV}` references, and these schema keys: `type`, `references`, `on_delete`, `on_update`, `primary_key`, `nullable`, `unique`, `index`, `rls_enabled`, `schema`, `public`, `file`, `runtime`, `method`, `returns`, `language`, `volatility`, `operations`, `version`, and `name` inside `fields` and `args`.
- SQL in `using`, `with_check`, `check`, `where`, `sql`, `body`, and function-call defaults such as `now()` is kept, but every string literal becomes `'<value>'`. Identifiers and expressions in that SQL are sent.
- Everything under an `env` map is redacted, including numbers, and so is everything reached through a YAML alias or `<<` merge key.
- If the file does not parse, it is not attached and is listed under `truncated_files` as unparseable.

Function files keep their code and line numbers, with these changes:
- Comment text is removed. The `//`, `/*`, and `*/` markers and the line breaks stay.
- String literals of 12 or more characters with no whitespace become `"<redacted>"`. Module names in `import` and `require` are kept, and so are object keys (a string after `{` or `,` and before `:`).
- Strings that contain spaces are kept, but every run of 12 or more non-space characters that mixes letters with digits or punctuation becomes `<redacted>`. So do `user:pass@` and `-p<password>`.
- Short strings are kept. The pattern rules from error messages then run over the result, so short passwords such as `"hunter2"` are only caught when they sit next to a name like `password`.

Redaction is best-effort. Use `v` to check the payload before you answer `y`, especially if your project keeps secrets in unusual places.

## `inz serve`

`inz serve` runs in production containers, CI, and Lambda, so it sends no usage events and shows no notice. It only reads `~/.instancez/telemetry.json` to honor `inz telemetry disable`, and writes to it only if you accept a crash prompt in an interactive terminal. A declined or unasked prompt sends nothing and leaves no files.

## Debug mode

Set `INSTANCEZ_TELEMETRY_DEBUG=1` to print each event as JSON to stderr. Nothing is sent. It works on source builds too.

```bash
INSTANCEZ_TELEMETRY_DEBUG=1 inz validate
```

## Where data goes

Events go to [PostHog](https://posthog.com), US region (`us.i.posthog.com`). Sending waits at most 2 seconds when a command exits. Network errors are silent and never fail a command.
