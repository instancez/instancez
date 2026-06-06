package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestNormalizeValue_UUIDToString(t *testing.T) {
	id := uuid.MustParse("3666de68-ee3d-46a0-a11e-a0f1944a417a")
	got := normalizeValue(pgtype.UUIDOID, [16]byte(id))
	if got != "3666de68-ee3d-46a0-a11e-a0f1944a417a" {
		t.Fatalf("uuid: got %#v, want canonical string", got)
	}
}

func TestNormalizeValue_NullUUIDStaysNil(t *testing.T) {
	if got := normalizeValue(pgtype.UUIDOID, nil); got != nil {
		t.Fatalf("NULL uuid should stay nil, got %#v", got)
	}
}

func TestNormalizeValue_NonUUIDPassthrough(t *testing.T) {
	if got := normalizeValue(pgtype.Int8OID, int64(2)); got != int64(2) {
		t.Fatalf("int8 passthrough: got %#v, want int64(2)", got)
	}
	// A [16]byte under a non-uuid OID must NOT be stringified.
	var b [16]byte
	if got := normalizeValue(pgtype.Int8OID, b); got != any(b) {
		t.Fatalf("non-uuid [16]byte should pass through unchanged, got %#v", got)
	}
}
