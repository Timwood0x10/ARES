package agentfabric

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Timwood0x10/ares/internal/core/models"
)

type stubTagger struct {
	caps []string
	err  error
}

func (s stubTagger) Tags(context.Context, string) ([]string, error) { return s.caps, s.err }

// TestCapabilitiesFor_PrefersPayloadThenTaggerThenKeywords pins the order and, more importantly,
// what happens on failure: a tagger that errors or returns nothing must fall back to keywords,
// never to "no capabilities" (which would advertise everything) or to a guess.
func TestCapabilitiesFor_PrefersPayloadThenTaggerThenKeywords(t *testing.T) {
	ctx := context.Background()

	// 1. Explicit payload tags win over both heuristics.
	task := models.NewTask("t1", "coder", nil)
	task.Payload[TaskToolCapabilityKey] = []string{"git"}
	task.Payload["request"] = "查询北京天气"
	c := &plannerCognition{tagger: stubTagger{caps: []string{"network"}}}
	assert.Equal(t, []string{"git"}, c.capabilitiesFor(ctx, task))

	// 2. A tagger's answer beats the keyword table.
	task2 := models.NewTask("t2", "coder", nil)
	task2.Payload["request"] = "查询北京天气"
	assert.Equal(t, []string{"network"}, c.capabilitiesFor(ctx, task2))
	// ...and the keyword table would have said the same here, so prove the tagger is the source
	// with an answer the keywords cannot produce.
	c.tagger = stubTagger{caps: []string{"pdf"}}
	assert.Equal(t, []string{"pdf"}, c.capabilitiesFor(ctx, task2))

	// 3. Failure falls back to keywords rather than narrowing to nothing.
	c.tagger = stubTagger{err: errors.New("model down")}
	assert.Equal(t, []string{"network"}, c.capabilitiesFor(ctx, task2))

	// 4. An empty answer also falls back.
	c.tagger = stubTagger{}
	assert.Equal(t, []string{"network"}, c.capabilitiesFor(ctx, task2))

	// 5. No tagger configured: keywords only (the default).
	c = &plannerCognition{}
	assert.Equal(t, []string{"network"}, c.capabilitiesFor(ctx, task2))
}

// TestParseCapabilityNames tolerates a fenced block but never invents names from prose: anything
// unparseable yields nil so the caller falls back to keywords.
func TestParseCapabilityNames(t *testing.T) {
	assert.Equal(t, []string{"network", "git"}, parseCapabilityNames(`["network","git"]`))
	assert.Equal(t, []string{"file"}, parseCapabilityNames("```json\n[\"file\"]\n```"))
	assert.Empty(t, parseCapabilityNames("I think it needs network access"))
	assert.Empty(t, parseCapabilityNames(""))
}
