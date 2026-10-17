package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var semverRe = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

// parseVersion turns "0.3.4" into a comparable triple.
func parseVersion(t *testing.T, s string) [3]int {
	t.Helper()
	m := semverRe.FindStringSubmatch(strings.TrimSpace(s))
	require.Len(t, m, 4, "%q is not a version", s)
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		require.NoError(t, err)
		out[i] = n
	}
	return out
}

func greaterVersion(a, b [3]int) bool {
	if a[0] != b[0] {
		return a[0] > b[0]
	}
	if a[1] != b[1] {
		return a[1] > b[1]
	}
	return a[2] > b[2]
}

// TestVersion_FileParses: the release artifact is stamped from this file, so a malformed
// value would ship a binary whose `ares version` is nonsense.
func TestVersion_FileParses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "VERSION"))
	require.NoError(t, err, "the VERSION file is the release's single source of truth")
	assert.NotEmpty(t, parseVersion(t, string(raw)))
}

// TestVersion_NoClaimIsAheadOfTheRelease is the drift guard.
//
// This repository cites versions in prose ("API frozen (0.3.4)", "pre-0.3.4 behaviour").
// When the file lags those claims, the built binary reports an OLDER version than the code
// and docs describe — the artifact disagrees with the release it belongs to, and nothing
// fails. It happened here: VERSION said 0.3.3 while README and several packages already
// claimed 0.3.4.
//
// The rule is one-directional on purpose: claims may be OLDER than VERSION (that is how
// "pre-0.3.3 behaviour" reads), never newer. A claim ahead of the release is a promise the
// artifact cannot keep.
func TestVersion_NoClaimIsAheadOfTheRelease(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "VERSION"))
	require.NoError(t, err)
	release := parseVersion(t, string(raw))

	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)

	var ahead []string
	for _, m := range semverRe.FindAllStringSubmatch(string(readme), -1) {
		claim := parseVersion(t, strings.Join(m[1:], "."))
		if greaterVersion(claim, release) {
			ahead = append(ahead, m[0])
		}
	}
	assert.Empty(t, ahead,
		"README claims %v but VERSION is %s — the built binary would report the older number", ahead, strings.TrimSpace(string(raw)))
}

// TestVersion_DefaultIsDev: the ldflags stamp is the only production source. A binary built
// without it must identify itself as "dev" rather than silently claiming a release number.
func TestVersion_DefaultIsDev(t *testing.T) {
	assert.Equal(t, "dev", version,
		"main.version must default to dev so an unstamped build is identifiable")
}
