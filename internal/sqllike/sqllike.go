// Package sqllike escapes user text for use inside a SQL LIKE pattern.
package sqllike

import "strings"

// Escape returns s with the LIKE metacharacters backslash, percent and
// underscore each prefixed by a backslash, so they match themselves. It is for
// use with ESCAPE '\' on the LIKE or ILIKE clause that receives the result.
func Escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
