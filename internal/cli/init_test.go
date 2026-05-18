package cli

import (
	"path/filepath"
	"testing"

	"github.com/saedx1/ultrabase/internal/config"
)

// TestRunInitScaffoldStartsCleanly guards the generated project: it must both
// validate AND describe migratable DDL. The todos.user_id FK must reference
// auth.users.id (3-part) — a 2-part `users.id` validates fine but the migrator
// resolves it to a nonexistent public.users table, so `ultrabase dev` would
// die at migration time.
func TestRunInitScaffoldStartsCleanly(t *testing.T) {
	dir := t.TempDir()
	if err := runInit("demo", dir); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	cfg, err := config.Load(filepath.Join(dir, "ultrabase.yaml"))
	if err != nil {
		t.Fatalf("load scaffolded config: %v", err)
	}
	if errs := config.Validate(cfg); errs != nil {
		t.Fatalf("scaffolded config failed validation: %v", errs)
	}

	todos, ok := cfg.Tables["todos"]
	if !ok {
		t.Fatal("scaffold missing todos table")
	}
	var fk string
	for _, f := range todos.Fields {
		if f.Name == "user_id" && f.ForeignKey != nil {
			fk = f.ForeignKey.References
		}
	}
	if fk != "auth.users.id" {
		t.Fatalf("todos.user_id FK references %q, want auth.users.id "+
			"(2-part users.id resolves to a nonexistent public.users table)", fk)
	}
}
