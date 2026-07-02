// Package truncate provides rune-safe string truncation utilities.
package truncate

// WithEllipsis truncates a string to at most maxLen runes, appending "..." if truncated.
// If maxLen <= 0, an empty string is returned.
func WithEllipsis(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
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
