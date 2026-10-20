package tokencount

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestText_UsesTheVendorRates pins the two rates the deployment standardises on: 0.6 token per
// CJK character and 0.3 per other character. A single rate would be wrong by 2x for one of the
// two classes, and mixed prompts (Chinese prose plus English code) are the normal case.
func TestText_UsesTheVendorRates(t *testing.T) {
	assert.InDelta(t, 1.2, Text("你好"), 1e-9, "two CJK characters cost 2x0.6")
	assert.InDelta(t, 1.2, Text("abcd"), 1e-9, "four ASCII characters cost 4x0.3")
	assert.InDelta(t, 1.8, Text("你好ab"), 1e-9, "mixed text sums the two classes: 2x0.6 + 2x0.3")

	assert.Equal(t, 2, Rounded("你好"), "rounding is upward, so a budget never under-counts")
	assert.Equal(t, 0, Rounded(""), "empty text costs nothing")
	assert.True(t, math.Abs(Text("a")-0.3) < 1e-9)
}
