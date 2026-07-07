package linker

import (
	"context"
	"sort"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// TimelineLinker generates chronological edges between objects (supersedes,
// generated_by) based on their CreatedAt timestamps.
type TimelineLinker struct{}

func (l *TimelineLinker) Name() string { return "timeline-linker" }

func (l *TimelineLinker) Link(_ context.Context, objects []*knowledge.KnowledgeObject) ([]knowledge.Relation, error) {
	if len(objects) < 2 {
		return nil, nil
	}

	// Sort by CreatedAt ascending.
	sorted := make([]*knowledge.KnowledgeObject, len(objects))
	copy(sorted, objects)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})

	var edges []knowledge.Relation

	// Connect each object to its chronologically nearest same-namespace neighbors.
	byNamespace := make(map[string][]*knowledge.KnowledgeObject)
	for _, obj := range sorted {
		ns := obj.Namespace
		if ns == "" {
			ns = "default"
		}
		byNamespace[ns] = append(byNamespace[ns], obj)
	}

	const weeksBetween = 2 * 7 * 24 * time.Hour

	for _, group := range byNamespace {
		for i := 1; i < len(group); i++ {
			prev, curr := group[i-1], group[i]
			if prev.CreatedAt.IsZero() || curr.CreatedAt.IsZero() {
				continue
			}
			diff := curr.CreatedAt.Sub(prev.CreatedAt)

			// Objects created close in time: generated_by.
			if diff > 0 && diff <= weeksBetween {
				edges = append(edges, knowledge.Relation{
					From:  curr.ID,
					To:    prev.ID,
					Name:  knowledge.RelGeneratedBy,
					Score: 0.7,
				})
			}

			// Objects created far apart: newer supersedes older.
			if diff > weeksBetween {
				edges = append(edges, knowledge.Relation{
					From:  curr.ID,
					To:    prev.ID,
					Name:  knowledge.RelSupersedes,
					Score: 0.5,
				})
			}
		}

		// Link the oldest and newest in the namespace.
		if len(group) >= 3 {
			first, last := group[0], group[len(group)-1]
			if !first.CreatedAt.IsZero() && !last.CreatedAt.IsZero() {
				edges = append(edges, knowledge.Relation{
					From:  last.ID,
					To:    first.ID,
					Name:  knowledge.RelSupersedes,
					Score: 0.3,
				})
			}
		}
	}

	return edges, nil
}

var _ runtime.Linker = (*TimelineLinker)(nil)
