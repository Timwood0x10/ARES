// Package truncate provides rune-safe string truncation utilities.
package truncate

// ellipsis is the suffix appended when a string is truncated.
const ellipsis = "..."

// WithEllipsis truncates a string to at most maxLen runes, appending "..." if
// truncated. The returned string (including the ellipsis) never exceeds maxLen
// runes. If maxLen is too small to fit the ellipsis (maxLen <= 3), the string
// is truncated without an ellipsis suffix.
// If maxLen <= 0, an empty string is returned.
func WithEllipsis(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	// When maxLen is too small to accommodate the ellipsis, truncate plainly.
	if maxLen <= len([]rune(ellipsis)) {
		return string(runes[:maxLen])
	}
	cut := maxLen - len([]rune(ellipsis))
	return string(runes[:cut]) + ellipsis
}

// Plain truncates a string to at most maxLen runes without adding an ellipsis.
// If maxLen <= 0, an empty string is returned.
func Plain(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
