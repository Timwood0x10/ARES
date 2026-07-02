// Package flight provides the public API for flight recording and observability.
package flight

import (
	"context"
	"time"

	aresflight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/ares_events"
)

// ---------------------------------------------------------------------------
// FlightRecorder
// ---------------------------------------------------------------------------

// FlightRecorder aggregates Timeline, Graph, Diagnostics, and Genealogy.
type FlightRecorder struct {
	inner *aresflight.FlightRecorder
}

// Config holds flight recorder dependencies.
type Config struct {
	EventStore ares_events.EventStore
	Genealogy  *Genealogy
}

// New creates a flight recorder.
func New(cfg Config) *FlightRecorder {
	g := (*aresflight.Genealogy)(nil)
	if cfg.Genealogy != nil {
		g = cfg.Genealogy.inner
	}
	inner := aresflight.NewFlightRecorder(aresflight.FlightRecorderConfig{
		EventStore: cfg.EventStore,
		Genealogy:  g,
	})
	return &FlightRecorder{inner: inner}
}

func (fr *FlightRecorder) Start(ctx context.Context) error { return fr.inner.Start(ctx) }
func (fr *FlightRecorder) Stop()                           { fr.inner.Stop() }
func (fr *FlightRecorder) Timeline() *Timeline            { return &Timeline{inner: fr.inner.Timeline()} }
func (fr *FlightRecorder) Graph() *Graph                  { return &Graph{inner: fr.inner.Graph()} }
func (fr *FlightRecorder) Diagnostics() *Diagnostics       { return &Diagnostics{inner: fr.inner.Diagnostics()} }

// ---------------------------------------------------------------------------
// Timeline
// ---------------------------------------------------------------------------

type Timeline struct{ inner *aresflight.Timeline }

func (tl *Timeline) Events() []TimelineEvent {
	evts := tl.inner.Events()
	out := make([]TimelineEvent, len(evts))
	for i, e := range evts {
		out[i] = TimelineEvent{
			ID: e.ID, AgentID: e.AgentID, ParentID: e.ParentID,
			Type: string(e.Type), Name: e.Name,
			StartAt: e.StartAt, EndAt: e.EndAt, Duration: e.Duration,
		}
	}
	return out
}

// TimelineEvent represents a single execution event.
type TimelineEvent struct {
	ID       string
	ParentID string
	AgentID  string
	Type     string
	Name     string
	StartAt  time.Time
	EndAt    time.Time
	Duration time.Duration
	Metadata map[string]any
}

// ---------------------------------------------------------------------------
// Graph
// ---------------------------------------------------------------------------

type Graph struct{ inner *aresflight.Graph }

type GraphNode struct {
	ID     string
	Type   string
	Status string
}

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

type Diagnostics struct{ inner *aresflight.DiagnosticsEngine }

// ---------------------------------------------------------------------------
// Genealogy
// ---------------------------------------------------------------------------

type Genealogy struct{ inner *aresflight.Genealogy }

func NewGenealogy() *Genealogy { return &Genealogy{inner: aresflight.NewGenealogy()} }

func (g *Genealogy) RecordSpawn(parentID, childID, agentType string, metadata map[string]any) {
	g.inner.RecordSpawn(parentID, childID, agentType, metadata)
}
func (g *Genealogy) RecordDeath(agentID string) { g.inner.RecordDeath(agentID) }
func (g *Genealogy) RecordResurrection(oldID, newID string) {
	g.inner.RecordResurrection(oldID, newID)
}
func (g *Genealogy) Roots() []LineageNode { return convertNodes(g.inner.Roots()) }

type LineageNode struct {
	ID       string
	Type     string
	ParentID string
	Children []LineageNode
	IsAlive  bool
}

func convertNodes(nodes []*aresflight.LineageNode) []LineageNode {
	out := make([]LineageNode, len(nodes))
	for i, n := range nodes {
		out[i] = LineageNode{
			ID: n.ID, Type: n.Type,
			ParentID: n.ParentID, Children: convertNodes(n.Children),
			IsAlive: n.IsAlive,
		}
	}
	return out
}
