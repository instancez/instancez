package app

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestGenerateRPCFunction_SetClauses(t *testing.T) {
	fn := domain.Function{Language: "sql", Volatility: "stable", Security: "definer", Returns: domain.FuncReturn{Type: "int"}, Body: "SELECT 1",
		Set: map[string]string{"work_mem": "64MB", "search_path": " public ,extensions ", "statement_timeout": "5s"}}
	got := generateRPCFunction("f", fn)
	want := "SECURITY DEFINER\nSET search_path = public, extensions\nSET statement_timeout = '5s'\nSET work_mem = '64MB'\nAS $ub$"
	if !strings.Contains(got, want) {
		t.Fatalf("got:\n%s", got)
	}
	fn.Set = map[string]string{"search_path": ""}
	if !strings.Contains(generateRPCFunction("f", fn), "SET search_path = ''\nAS") {
		t.Fatal("empty search_path must render ''")
	}
	fn.Set = nil
	if strings.Contains(generateRPCFunction("f", fn), "SET ") {
		t.Fatal("no set: must emit no SET")
	}
}
