package output

import (
	"encoding/json"
	"io"
)

// JSON writes the report as indented JSON. The shape is the Report struct,
// which is also what the table renders from, so the two views can never
// disagree about what was found.
func JSON(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}
