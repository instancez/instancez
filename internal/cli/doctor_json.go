package cli

import (
	"encoding/json"
	"io"

	"github.com/instancez/instancez/internal/cli/preflight"
)

type doctorCheckJSON struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

func renderDoctorJSON(w io.Writer, results []preflight.Result) error {
	checks := make([]doctorCheckJSON, 0, len(results))
	for _, r := range results {
		checks = append(checks, doctorCheckJSON{r.Name, r.OK, r.Detail, r.FixHint})
	}
	return json.NewEncoder(w).Encode(struct {
		OK     bool              `json:"ok"`
		Checks []doctorCheckJSON `json:"checks"`
	}{!preflight.AnyFailed(results), checks})
}
