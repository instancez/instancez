---
title: Coding Agents
description: Install the instancez skill into Claude Code, Codex, Cursor, and other coding agents so they know the YAML syntax, RLS patterns, and the inz CLI.
---

instancez is built to be written by agents: the whole backend is one YAML file an agent can read end to end. The repo ships an [agent skill](https://github.com/instancez/instancez/blob/main/skills/instancez/SKILL.md) that teaches your agent the `instancez.yaml` syntax, RLS patterns, the `rpc:` vs `functions:` split, and the `inz` CLI, so it stops guessing and starts running `inz validate`.

## Install with the skills CLI (any agent)

The [skills CLI](https://github.com/vercel-labs/skills) installs the skill from this repo into whichever agents it detects in your machine or project:

```bash
npx skills add instancez/instancez
```

Run it in your project directory. It auto-detects installed agents and prompts if it finds none. To target specific agents:

```bash
npx skills add instancez/instancez -a claude-code   # -> .claude/skills/
npx skills add instancez/instancez -a codex         # -> .agents/skills/
npx skills add instancez/instancez -a cursor        # -> .agents/skills/
npx skills add instancez/instancez -a opencode      # -> .agents/skills/
npx skills add instancez/instancez -a gemini-cli    # -> .agents/skills/
npx skills add instancez/instancez -a github-copilot # -> .agents/skills/
```

Add `-g` to install globally (for example `~/.claude/skills/`) instead of into the current project.

## Claude Code plugin

Claude Code users can install it as a plugin instead, straight from this repo:

```
/plugin marketplace add instancez/instancez
/plugin install instancez@instancez
```

The plugin route keeps the skill updated with the marketplace; the skills CLI route vendors a copy into your project.

## Manual (everything else)

The skill is a single Markdown file, so any agent that reads project context can use it. Download it and point your agent's instructions file at it:

```bash
curl -fsSL https://raw.githubusercontent.com/instancez/instancez/main/skills/instancez/SKILL.md \
  -o docs/instancez-skill.md
```

Then add a line to your `AGENTS.md` (or `CLAUDE.md`, `.cursorrules`, ...):

```markdown
When working on the instancez backend (instancez.yaml, functions/), read docs/instancez-skill.md first.
```

## What the skill covers

- The edit loop: `inz init`, edit `instancez.yaml`, `inz validate`, `inz dev`
- Table syntax: fields, types, defaults, enums, foreign keys, indexes
- RLS policies: the three roles, `auth.uid()` helpers, common patterns, and the `update` policy pitfall
- Auth, storage buckets, SQL `rpc:`, and Node.js `functions:` with the handler contract
- Deploying with `inz bundle`, `inz serve`, and `inz cloud deploy`

It also carries the rules agents tend to trip on: no auto-added `id` columns, YAML removals become DROPs, and the `auth`/`storage` schemas are reserved.
