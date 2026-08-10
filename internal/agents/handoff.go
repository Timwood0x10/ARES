// Package agents provides Handoff — explicit context transfer between agent roles.
//
// Design principle (from AI Agents in Depth Ch.10):
// "Agents don't share full conversation history. They exchange structured
// handoffs containing only what the next role needs to know."
package agents

import (
	"fmt"
	"time"
)

// ArtifactRef references a piece of work produced by one agent and consumed by another.
type ArtifactRef struct {
	// Path is the file path or memory key for the artifact.
	Path string

	// Type describes what kind of artifact this is.
	// Values: "file", "data", "summary", "code", "report"
	Type string

	// Summary is a one-line description for the receiving agent.
	// Example: "Research findings on EV battery trends 2021-2023"
	Summary string
}

func (a *ArtifactRef) String() string {
	return fmt.Sprintf("%s:%s (%s)", a.Type, a.Path, a.Summary)
}

// Handoff is the unit of collaboration between specialized agents.
// It replaces implicit context sharing with explicit, bounded transfers.
type Handoff struct {
	// From is the role ID of the sending agent.
	From string

	// To is the role ID of the receiving agent.
	To string

	// Task describes what was accomplished and what remains.
	Task string

	// Context carries structured data the next role needs.
	// Unlike full conversation history, this is curated by the sender.
	Context map[string]any

	// Artifacts are the tangible outputs being transferred.
	Artifacts []ArtifactRef

	// CreatedAt records when this handoff was created.
	CreatedAt time.Time

	// Metadata carries optional fields for routing or logging.
	Metadata map[string]any
}

// NewHandoff creates a new Handoff from sender to receiver.
func NewHandoff(from, to, task string) *Handoff {
	return &Handoff{
		From:      from,
		To:        to,
		Task:      task,
		Context:   make(map[string]any),
		Artifacts: make([]ArtifactRef, 0),
		CreatedAt: time.Now(),
		Metadata:  make(map[string]any),
	}
}

// WithContext adds structured context to the handoff.
func (h *Handoff) WithContext(key string, value any) *Handoff {
	h.Context[key] = value
	return h
}

// WithArtifact adds an artifact reference to the handoff.
func (h *Handoff) WithArtifact(path, atype, summary string) *Handoff {
	h.Artifacts = append(h.Artifacts, ArtifactRef{
		Path:    path,
		Type:    atype,
		Summary: summary,
	})
	return h
}

// WithMetadata adds metadata for routing or observability.
func (h *Handoff) WithMetadata(key string, value any) *Handoff {
	h.Metadata[key] = value
	return h
}

// HasArtifact checks if the handoff contains an artifact of the given type.
func (h *Handoff) HasArtifact(atype string) bool {
	for _, a := range h.Artifacts {
		if a.Type == atype {
			return true
		}
	}
	return false
}

// ArtifactOfType returns the first artifact of the given type.
func (h *Handoff) ArtifactOfType(atype string) *ArtifactRef {
	for i := range h.Artifacts {
		if h.Artifacts[i].Type == atype {
			return &h.Artifacts[i]
		}
	}
	return nil
}

// Size returns the estimated size of the handoff in terms of artifact count
// and context keys. Used for budget tracking.
func (h *Handoff) Size() int {
	return len(h.Artifacts) + len(h.Context)
}

// String returns a human-readable summary of the handoff.
func (h *Handoff) String() string {
	return fmt.Sprintf("Handoff{%s→%s: %d artifacts, %d context keys}",
		h.From, h.To, len(h.Artifacts), len(h.Context))
}
