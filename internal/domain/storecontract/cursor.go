package storecontract

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EventCursorLayout is the fixed-width UTC layout for event times and cursors,
// so string order is time order.
const EventCursorLayout = "2006-01-02T15:04:05.000000000Z"

// cursorSeparator joins the sort key and the row id inside a cursor. The sort
// key never contains it, so splitting on the last occurrence is unambiguous.
const cursorSeparator = "|"

// cursorIDWidth zero-pads the row id so the whole cursor is lexicographically
// sortable: the fixed-width sort key is followed by a fixed-width tie-break,
// and string comparison of two cursors is the total order (key, id).
const cursorIDWidth = 20

// EventCursor returns the opaque resume cursor for an event that happened at
// at with row id id. The cursor is a string so EventSource can carry it
// verbatim in its id: field and echo it back as the Last-Event-ID header; it
// is opaque to every consumer except EventsSince.
func EventCursor(at time.Time, id int64) string {
	return at.UTC().Format(EventCursorLayout) + cursorSeparator + fmt.Sprintf("%0*d", cursorIDWidth, id)
}

// ParseEventCursor splits a cursor back into its sort key (the fixed-width at
// string) and row id. ok is false for an empty or malformed cursor, which
// means "from the beginning" -- the store must not treat a bad resume point as
// an error worth failing a feed over.
func ParseEventCursor(cursor string) (at string, id int64, ok bool) {
	if cursor == "" {
		return "", 0, false
	}
	i := strings.LastIndex(cursor, cursorSeparator)
	if i < 0 {
		return "", 0, false
	}
	id, err := strconv.ParseInt(cursor[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return cursor[:i], id, true
}
