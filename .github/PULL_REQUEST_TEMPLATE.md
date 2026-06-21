## What this changes

<!-- A short description of the change and why it is needed. Link any related issue, e.g. "Fixes #123". -->

## Checklist

- [ ] `go build ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `go test -tags=integration -race ./...` passes for the packages I touched (needs Docker)
- [ ] `npm test` passes in `dashboard/`, if I changed the frontend
- [ ] The supabase-js compatibility test still passes, if I touched the HTTP surface, auth, RPC, storage, or JWT handling
- [ ] Docs and examples in `docs/site/` are updated, if behavior or config changed
- [ ] Commits are signed off (`git commit -s`)

## Notes for reviewers

<!-- Anything worth flagging: tradeoffs, things you are unsure about, areas that need a closer look. -->
