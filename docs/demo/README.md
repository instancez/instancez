# Interactive demo

Source for the demo at `/interactive-demo/` in the docs site. `build.py` inlines the captures, screenshots and fonts into one file, `docs/site/public/demo/instancez-demo.html`.

```sh
python3 docs/demo/build.py
```

## What is in here

- `captures/*.raw`: terminal output recorded from a real local `inz` instance through a pty (`script -qec`), including ANSI colors.
- `captures/*.yaml`, `app.mjs`: the project files shown in the editor panes (`yaml1` is `yaml0` plus the `priority` field).
- `shots/*.webp`: dashboard screenshots from the same instance (1440x900 at 2x, dark mode).
- `assets/fonts`: Bricolage Grotesque and JetBrains Mono, both under the SIL Open Font License.
- `player.src.html`: the player. Chapters and callouts are plain data near the top of its script.

## Refresh the captures

Do this when CLI output or the dashboard changes.

1. Build `inz`, make an empty directory, and run `inz init`. Record each command with `script -qec "<command>" /dev/null > captures/<name>.raw`.
2. Start `inz dev --embedded-pg`, add `priority` to the `todos` fields (see `captures/yaml1.yaml`), and record the "config reloaded" log lines.
3. For `serve`, run against a throwaway Postgres (for example `docker run postgres:16`) with `--migrate` and explicit keys, on a project without code functions. Keep the log up to `HTTP server listening`.
4. Capture dashboard screenshots at 1440x900 with device scale factor 2. The callout rectangles in `player.src.html` are in 1440x900 CSS pixels, so re-check them if the layout moves.
5. Run `python3 docs/demo/build.py`, open the page, and click through every chapter.

The `inz cloud deploy` sessions are illustrative (they need an account), so they live in `player.src.html` and are built from the strings in `internal/cli/deploy.go`.
