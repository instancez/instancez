# Strict unknown-key validation for instancez config

Date: 2026-06-20
Status: approved, pre-implementation

## Why

`instancez.yaml` is the source of truth for a project. Today the loader decodes
it with `yaml.Unmarshal`, which silently drops any key that does not map to a
struct field. A typo (`tabels:` instead of `tables:`), a stale key from an older
schema, or a removed key like `auth.google` (just moved to `auth.oauth.<name>`)
parses cleanly and is ignored. The author gets no error and the behavior they
expected never happens. The dashboard's JSON `PUT /config` path has the same
gap: `encoding/json` ignores unknown fields too.

For an open-source release this is a sharp edge. We want every unrecognized key,
on every config surface, to fail with a readable message that points at the
offending key, rather than disappear.

## Scope

In scope: reject unknown keys on all YAML decode paths (boot, `inz validate`,
`deploy`, dashboard read-back and preview) and the dashboard's JSON `PUT /config`
/ `POST /config/preview` edit path. Produce readable errors through the existing
channels. Out of scope: changing the schema itself, validating values (the
existing `Validate` already does that), or new config keys.

## Mechanism

Strict decoding replaces the lenient unmarshals, reusing the library flags that
already understand the struct-vs-map distinction:

- **YAML**: `yaml.NewDecoder(r)` with `dec.KnownFields(true)`, used inside
  `ParseBytes`, `ParseBytesLenient`, and `ParseBytesRaw` (`internal/config/loader.go`).
- **JSON**: a new `config.UnmarshalConfigJSON(data []byte) (*domain.Config, error)`
  using `json.NewDecoder` + `dec.DisallowUnknownFields()`. It replaces the
  `json.Unmarshal` in `pkg/configvalidate.MarshalYAML` and the `ShouldBindJSON`
  config-body binds in the admin `handlePutConfig` and `handlePreviewConfig`.

Both flags reject unknown **struct** fields while leaving map-keyed sections
open. The config's map sections (`tables`, `storage`, `rpc`, `functions`,
`auth.oauth`, `auth.email.templates`) keep arbitrary user keys. This was
verified against `domain.Config`: there is no `,inline` field and no top-level
catch-all, and the two `map[string]any` fields (`Field.Extra`, `*.Metadata`) are
not YAML-tagged config inputs.

A custom YAML-node walker was rejected: it would have to re-implement yaml tag
handling (inline, omitempty, the map cases) and is more code and more risk than
the battle-tested library flags, which also hand back line numbers.

## Readable errors

A translator in `internal/config` converts the raw library errors into clean
messages and reports every offending key, not just the first:

- yaml.v3 produces `yaml: unmarshal errors:\n  line 48: field google not found in
  type domain.Auth`. The translator parses each sub-line into
  `line 48: unknown key "google" under auth`. The section name comes from a small
  Go-type-to-path map (`domain.Config` is the top level, `domain.Auth` is `auth`,
  `domain.Server` is `server`, and so on); when a type is not in the map the
  message falls back to `unknown key "google"`.
- `encoding/json` produces `json: unknown field "google"`, which becomes
  `unknown key "google"`. The standard library does not expose a line number for
  this case.

The messages reach users through the channels that already exist. `inz validate`
prints the parse error. `pkg/configvalidate.ValidateYAML` already wraps a parse
failure as a `Problem{Message: err.Error()}`, so the platform dashboard renders
the readable text. The admin PUT path returns the message in its error envelope.

## Invariants

- `${VAR}` interpolation still runs before decoding, so strictness does not
  interfere with env references (the lenient path substitutes placeholders
  first, then decodes).
- Map-keyed sections keep arbitrary keys.
- No schema or wire-format change; valid configs parse exactly as before.

## Testing

Test-driven. Each step writes a failing test first.

- Unit (YAML): `auth.google: {…}` errors with a message naming `google`; a
  typo'd top-level key (`tabels:`) errors; a valid config full of map keys
  (several `tables`, `storage`, `auth.oauth.<name>`) still parses; multiple
  unknown keys are all reported.
- Unit (JSON): `UnmarshalConfigJSON` rejects an unknown field and accepts a valid
  config.
- Translator: a `yaml.TypeError` with a known type maps to `under <section>`; an
  unknown type falls back to the bare key.
- Regression (the real risk): enabling strictness can surface pre-existing stray
  keys in test fixtures, the repo's own `instancez.yaml`, or the platform's
  stored config. Each one is a real bug being exposed and gets fixed (fixture or
  struct), which may widen the diff. Gated by the full unit suite, the
  integration suite (including `TestSupabaseJSCompat`), `inz validate` on the
  repo config, and the platform's `data` module tests.

## Sequencing

1. The error translator (pure, unit-tested in isolation).
2. Strict YAML decode in the three `ParseBytes*` functions, wired to the
   translator. Run the full suite and fix any fixture the new strictness exposes.
3. Strict JSON decode (`UnmarshalConfigJSON`) and its three call sites.
4. Confirm `inz validate`, the integration suite, and the platform tests are
   green.
