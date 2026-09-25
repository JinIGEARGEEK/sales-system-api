package utils

import "strings"

// likeEscaper escapes LIKE's metacharacters with backslash.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// LikePattern turns a user-supplied search term into a substring pattern
// that matches it literally: %, _ and \ in v are escaped, then the result is
// wrapped in %…%. Bind it to `ILIKE ? ESCAPE '\'` — backslash is already
// Postgres's default LIKE escape, but the codebase names it explicitly.
func LikePattern(v string) string {
	return "%" + likeEscaper.Replace(v) + "%"
}
