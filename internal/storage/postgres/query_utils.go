package postgres

import "strings"

// EscapeILIKEPattern escapes ILIKE wildcard characters in user input.
// This prevents users from injecting % or _ wildcards to broaden search results.
// The function escapes backslashes first, then % and _ characters.
func EscapeILIKEPattern(input string) string {
	input = strings.ReplaceAll(input, `\`, `\\`)
	input = strings.ReplaceAll(input, `%`, `\%`)
	input = strings.ReplaceAll(input, `_`, `\_`)
	return input
}
