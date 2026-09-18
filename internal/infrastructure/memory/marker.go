package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The engine writes each record as one blank-line-delimited markdown block
// whose first line is a marker comment followed by the record's content, so
// a block stays readable and editable by hand. The marker began life as a
// bare id (`<!--mem:<ulid>-->`, the incumbent format); it is JSON now
// because the retained provenance -- revision, kind, author, originating
// user, source and both timestamps -- has nowhere else to live, which is
// exactly what the PRD's Settles table records the bare form could not
// carry.
const (
	markerPrefix = "<!--mem:"
	markerSuffix = "-->"
)

// markerData is the provenance one block's marker carries. The same shape is
// written into a scope's live document and into its HISTORY.md; the last two
// fields are set only on a retained state, so a live block never carries
// them.
//
// json escapes newlines, so the marker is always exactly one line: a field
// containing one cannot break the block's first-line shape.
type markerData struct {
	ID         string    `json:"id"`
	Revision   int       `json:"revision"`
	Kind       string    `json:"kind"`
	Author     string    `json:"author,omitempty"`
	OriginUser string    `json:"origin_user,omitempty"`
	Source     string    `json:"source,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	// SupersededAt is when this state stopped being live. Set only on a
	// state written to HISTORY.md.
	SupersededAt *time.Time `json:"superseded_at,omitempty"`
	// Deleted marks the state Forget retained: the record's last live
	// content, kept so its provenance outlives it. Set only in HISTORY.md.
	Deleted bool `json:"deleted,omitempty"`
}

// renderMarker encodes one marker line.
func renderMarker(m markerData) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The marker is documented as hand-editable, so it must not be
	// HTML-escaped into \u003c runs an operator cannot read back. The suffix
	// is still unambiguous either way: the body is cut at the line's end.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return "", fmt.Errorf("memory: builtin engine: encoding a record marker: %w", err)
	}
	return markerPrefix + strings.TrimRight(buf.String(), "\n") + markerSuffix, nil
}

// parseMarker decodes a block's first line. ok is false for a line that is
// not a marker, or one that names no record: a block without a usable marker
// is not one this engine ever wrote.
func parseMarker(line string) (markerData, bool) {
	body, ok := strings.CutPrefix(line, markerPrefix)
	if !ok {
		return markerData{}, false
	}
	body, ok = strings.CutSuffix(body, markerSuffix)
	if !ok {
		return markerData{}, false
	}
	var m markerData
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return markerData{}, false
	}
	if m.ID == "" {
		return markerData{}, false
	}
	return m, true
}
