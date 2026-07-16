# Records the terminal stage (yaml + inz dev booting) of
# docs/site/src/assets/demo.gif shown at the top of README.md.
#
# Requires: vhs (charmbracelet/vhs), tmux, bat, and inz on PATH, plus a
# reachable Postgres. Point INSTANCEZ_DATABASE_URL below at yours.
#
# Run from anywhere: vhs demo.tape
#
# The shipped gif has a second stage after this one: a real webpage (a
# minimal supabase-js CRUD app) driven by Playwright — including a real click
# on a "Sign in anonymously" button, so the identity behind every later call
# is visible on screen — composited side by side with static
# bat --highlight-line stills of its code so the highlighted line tracks
# whichever call (auth/select/insert/update/delete) is firing. That stage
# isn't captured by this tape — it's assembled from a separate Playwright
# script and a few `bat -H <line>` renders, then stitched together with
# ffmpeg. This tape covers the reproducible half.
#
# Pacing note: the last terminal frame and the first browser/code frame are
# each held for an extra ~15-20 frames (transition beat), and the browser/
# code composite frames are each duplicated 5x on encode (not 3x) — slower
# reading pace per the "hard to read" feedback on an earlier cut.

Output demo-terminal.gif
Require tmux
Set FontSize 17
Set Width 1400
Set Height 970
Set Theme "Dracula"
Set Shell bash

Env INSTANCEZ_DATABASE_URL "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
Env PS1 "> "
Env BAT_PAGER ""
Env BAT_STYLE "numbers"

Hide
  Type@1ms `tmux -f /dev/null -L demo kill-server 2>/dev/null; rm -rf /tmp/vhsdemo && mkdir -p /tmp/vhsdemo && cd /tmp/vhsdemo` Enter
  Type@1ms `printf 'version: 1\n\nproject:\n  name: demo\n\nproviders:\n  storage:\n    type: local\n\nauth:\n  jwt_expiry: 15m\n  refresh_tokens: true\n  allow_anonymous: true\n\ntables:\n  todos:\n    fields:\n      - name: id\n        type: bigserial\n        primary_key: true\n      - name: user_id\n        foreign_key:\n          references: auth.users.id\n          on_delete: cascade\n      - name: title\n        type: text\n        required: true\n      - name: status\n        type: text\n        enum: [pending, active, done]\n        default: pending\n    rls:\n      - operations: [select, insert, update, delete]\n        using: "user_id = auth.uid()"\n        with_check: "user_id = auth.uid()"\n' > instancez.yaml` Enter
  Type "clear" Enter
  Type "tmux -f /dev/null -L demo new-session -- bash --norc" Enter
  Type "tmux split-window -d -h -- bash --norc" Enter
  Type "tmux set status off" Enter
  Type 'tmux setw pane-border-style "fg=8"' Enter
  Type 'tmux setw pane-active-border-style "fg=8"' Enter
  Ctrl+L
  Sleep 500ms
Show

Ctrl+B
Type "o"
Sleep 500ms

Type "bat instancez.yaml"
Enter
Sleep 2.5s

Ctrl+B
Type "o"
Sleep 500ms

Type "inz dev"
Enter
Sleep 3s
