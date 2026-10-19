package agentfabric

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Timwood0x10/ares/internal/llmcore"
)

// roleUser is the chat role every injected prompt uses; declared once because goconst counts
// these per package and a typo in one of them would produce a silently-ignored message.
const roleUser = "user"

// CapabilityTagger derives a task's capability tags from its request text.
//
// The keyword table is the default and is deliberately narrow, which leaves a gap: a task that
// mentions none of its terms is advertised every tool (correct but expensive), and one that
// mentions only some of them is narrowed to a set that may be missing a tool it needs. A
// tagger can close that gap with a model call — at the cost of one round-trip per quantum,
// which is why it is opt-in.
type CapabilityTagger interface {
	Tags(ctx context.Context, text string) ([]string, error)
}

// ToolAdvertisementSink receives one record per advertisement (M10/P2 observability).
//
// It is a small purpose-built interface rather than the event store itself: the planner has no
// business knowing about streams and versions, and the caller that owns the store can turn this
// into whatever it records.
type ToolAdvertisementSink interface {
	OnToolAdvertisement(ctx context.Context, sessionID, taskID string, caps []string, advertised, total int)
}

// llmCapabilityTagger asks the model which capabilities a request needs.
type llmCapabilityTagger struct {
	chat ChatClient
}

// NewLLMCapabilityTagger returns a tagger backed by the given client, or nil when there is no
// client — a nil tagger is the signal for "use the keyword table".
func NewLLMCapabilityTagger(chat ChatClient) CapabilityTagger {
	if chat == nil {
		return nil
	}
	return &llmCapabilityTagger{chat: chat}
}

// Tags asks for capability names from the same vocabulary the tools declare.
//
// The prompt pins the vocabulary and forbids prose because the answer is parsed; a model that
// replies with a sentence would otherwise be read as "no capabilities", silently falling back
// to the keyword table with no trace of why.
func (t *llmCapabilityTagger) Tags(ctx context.Context, text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	prompt := []*llmcore.LLMMessage{{
		Role: roleUser,
		Content: "Which capabilities does this task need? Choose only from: network, file, math, data, text, git, shell, execution, system, embedding, crypto, pdf. " +
			"Reply with a JSON array of names and nothing else. If none apply, reply [].\n\nTask:\n" + text,
	}}
	resp, err := t.chat.Chat(ctx, prompt, nil, map[string]any{"temperature": 0.0})
	if err != nil || resp == nil {
		return nil, err
	}
	return parseCapabilityNames(resp.Content), nil
}

// parseCapabilityNames reads the model's JSON array, tolerating a fenced block. Anything
// unparseable yields nil so the caller falls back to keywords instead of narrowing to nothing.
func parseCapabilityNames(content string) []string {
	s := strings.TrimSpace(content)
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j > i {
			s = s[i : j+1]
		}
	}
	var names []string
	if err := json.Unmarshal([]byte(s), &names); err != nil {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}
