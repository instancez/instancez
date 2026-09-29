//go:build integration

package app_test

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
)

const docsTodosTable = `version: 1
tables:
  todos:
    fields:
      - { name: id, type: bigserial, primary_key: true }
      - { name: team_id, type: bigint }
`

var rpcExample = regexp.MustCompile("(?s)```yaml\n(rpc:\n.*?)```")

func TestIntegration_DocumentedRPCExamplesDeploy(t *testing.T) {
	for _, path := range []string{
		"../../docs/site/src/content/docs/api-reference/rpc.md",
		"../../skills/instancez/SKILL.md",
	} {
		t.Run(path, func(t *testing.T) {
			md, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			blocks := rpcExample.FindAllSubmatch(md, -1)
			if len(blocks) == 0 {
				t.Fatal("no rpc yaml example found")
			}
			for _, b := range blocks {
				cfg, err := config.ParseBytes([]byte(docsTodosTable+string(b[1])), path)
				if err != nil {
					t.Fatalf("parse example: %v\n%s", err, b[1])
				}
				if err := app.NewMigrator(startPostgres(t)).Apply(context.Background(), cfg); err != nil {
					t.Fatalf("deploy example: %v\n%s", err, b[1])
				}
			}
		})
	}
}
