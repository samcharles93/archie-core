package postgres

import (
	"errors"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// clip limits s to n bytes without splitting a UTF-8 rune. It is the exact
// write-side normalisation the SQLite store applied before inserting event
// detail, transition detail, park reasons and review payloads: a generated
// query that drops it lets an oversized value through where the old store
// truncated it.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	pos := 0
	for pos < len(s) {
		_, size := utf8.DecodeRuneInString(s[pos:])
		if pos+size > n {
			break
		}
		pos += size
	}
	return s[:pos]
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505), matching the SQLite store's "UNIQUE constraint
// failed" string test rather than grepping driver error text.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
