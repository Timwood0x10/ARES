package linker

import (
	"context"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// ArchitectureLinker generates dependency relations between objects tagged
// as code entities (functions, structs, interfaces) and architecture/design
// decisions.
type ArchitectureLinker struct{}

func (l *ArchitectureLinker) Name() string { return "architecture-linker" }

func (l *ArchitectureLinker) Link(_ context.Context, objects []*knowledge.KnowledgeObject) ([]knowledge.Relation, error) {
	var codeObjs, archObjs []*knowledge.KnowledgeObject
	codeTypes := map[knowledge.ObjectType]bool{
		knowledge.ObjectCode:     true,
		knowledge.ObjectDocument: true,
		knowledge.ObjectDecision: true,
	}

	for _, obj := range objects {
		if codeTypes[obj.Type] {
			codeObjs = append(codeObjs, obj)
		}
		if obj.Type == knowledge.ObjectDecision ||
			obj.Type == knowledge.ObjectDocument ||
			strings.Contains(strings.ToLower(obj.Summary), "architecture") {
			archObjs = append(archObjs, obj)
		}
	}

	var edges []knowledge.Relation

	// Code objects depend on architecture objects.
	for _, code := range codeObjs {
		for _, arch := range archObjs {
			if code.ID == arch.ID {
				continue
			}
			// Skip when either object has no tags — no-tag objects lack
			// semantic anchor and would create misleading edges.
			if len(code.Tags) == 0 || len(arch.Tags) == 0 {
				continue
			}
			if hasOverlap(code.Tags, arch.Tags) {
				edges = append(edges, knowledge.Relation{
					From:  code.ID,
					To:    arch.ID,
					Name:  knowledge.RelDependsOn,
					Score: 0.6,
				})
			}
		}
	}

	return edges, nil
}

var _ runtime.Linker = (*ArchitectureLinker)(nil)
