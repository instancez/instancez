package cli

import (
	"encoding/json"
	"io"

	"github.com/instancez/instancez/internal/cli/preflight"
)

func renderDoctorJSON(w io.Writer, results []preflight.Result) error {
	if results == nil {
		results = []preflight.Result{}
	}
	return json.NewEncoder(w).Encode(struct {
		OK     bool               `json:"ok"`
		Checks []preflight.Result `json:"checks"`
	}{!preflight.AnyFailed(results), results})
}
