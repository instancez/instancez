// Package dashboard embeds the built SPA assets into the Go binary so a
// single `instancez` executable can serve the dashboard at /dashboard/*
// without a separate Node process. The asset tree comes from `npm run
// build` inside this directory (produces dist/). Run `make build` from
// the repo root to chain the npm build into a release binary.
//
// On a fresh checkout dist/ contains only .gitkeep, so Assets() returns
// nil and the HTTP layer falls through to its existing nil-handling
// (dev mode redirects to the Vite dev server; serve mode returns 404).
package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Assets returns the built SPA rooted at dist/, or nil when no build
// output is present (the .gitkeep stub is the only entry).
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
