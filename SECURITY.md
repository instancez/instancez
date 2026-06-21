# Security policy

instancez handles authentication, JWTs, row-level security, and file storage, so we take security reports seriously and want to hear about problems before they are public.

## Reporting a vulnerability

Please do not open a public issue for a security problem.

Report it privately through GitHub's [private vulnerability reporting](https://github.com/instancez/instancez/security/advisories/new) on this repository. <!-- TODO(security): enable "Private vulnerability reporting" in repo Settings > Security before launch. -->

<!-- TODO(security): if you prefer email instead of or in addition to GitHub advisories,
     add a dedicated address here, e.g. security@instancez.io, and remove this comment. -->

Include as much as you can:

- A description of the issue and its impact.
- Steps to reproduce, or a proof of concept.
- The version or commit you tested.
- Any suggested fix, if you have one.

## What to expect

- We aim to acknowledge a report within a few business days.
- We will confirm the issue, work on a fix, and keep you updated on progress.
- Once a fix is released, we will credit you in the advisory unless you ask us not to.

## Supported versions

instancez is pre-1.0 in practice and moves quickly. Security fixes land on the latest release. Please test against a recent version before reporting.

<!-- TODO(security): once a formal release cadence exists, replace the line above with a supported-versions table. -->

## Scope

In scope: the instancez server (`inz`), the HTTP API surface, auth and JWT handling, RLS enforcement behavior, storage access control, and the dashboard.

Out of scope: vulnerabilities in your own `instancez.yaml` policies or in third-party dependencies that should be reported upstream, though we still want to know if a dependency issue affects instancez directly.
