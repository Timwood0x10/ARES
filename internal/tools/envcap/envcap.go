// Package envcap provides agent-bundled environment capability retrieval
// (primitive 7 completion): a running agent can search the machine's
// environment — registered tools (builtin + MCP), skills, and allowlisted
// native commands — through one unified entry point. This mirrors cc-switch's
// skill search (filter by name/description) but across the whole environment,
// and pairs with progressive disclosure: search returns name + description,
// full details load on demand.
package envcap

import (
	"context"
	"sort"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge/skills"
	"github.com/Timwood0x10/ares/internal/tools/discovery"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// Kind classifies a discovered environment capability.
type Kind string

const (
	// KindTool is a registered tool (builtin or MCP).
	KindTool Kind = "tool"
	// KindSkill is a registered skill.
	KindSkill Kind = "skill"
	// KindCommand is a native host command from the allowlist.
	KindCommand Kind = "command"
)

// Capability is one searchable environment capability. Description is the
// always-resident one-liner; detail (skill body / command semantics) loads on
// demand by Name.
type Capability struct {
	Kind        Kind
	Name        string
	Description string
}

// ToolLister returns the currently registered tools (builtin + MCP). It is
// satisfied by *core.Registry (via List/Get) or toolsource.ToolSource.
type ToolLister interface {
	// Tools returns a snapshot of currently available tools.
	Tools(ctx context.Context) ([]core.Tool, error)
}

// Searcher aggregates tool, skill, and native-command sources into one
// environment capability search. It is safe for concurrent use (sources own
// their locking).
type Searcher struct {
	tools  ToolLister
	skills *skills.Registry
	cmds   *discovery.Discoverer // may be nil (no native command probing)
}

// NewSearcher creates a Searcher over the given sources. tools and skills may
// be nil (their sources are skipped); cmds may be nil to disable native
// command discovery.
func NewSearcher(tools ToolLister, skills *skills.Registry, cmds *discovery.Discoverer) *Searcher {
	return &Searcher{tools: tools, skills: skills, cmds: cmds}
}

// Search queries all configured sources for capabilities whose name or
// description contains the query (case-insensitive). Results are ranked by
// kind (tool, skill, command) and sorted by name within each kind. Limit <= 0
// returns all matches.
func (s *Searcher) Search(ctx context.Context, query string, limit int) ([]Capability, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}

	var out []Capability
	if s.tools != nil {
		if tools, err := s.tools.Tools(ctx); err == nil {
			for _, t := range tools {
				if strings.Contains(strings.ToLower(t.Name()), q) ||
					strings.Contains(strings.ToLower(t.Description()), q) {
					out = append(out, Capability{Kind: KindTool, Name: t.Name(), Description: t.Description()})
				}
			}
		}
	}
	if s.skills != nil {
		for _, sk := range s.skills.Search(query, 0) {
			out = append(out, Capability{Kind: KindSkill, Name: sk.Name, Description: sk.Description})
		}
	}
	if s.cmds != nil {
		if cmds, err := s.cmds.Discover(ctx); err == nil {
			for _, c := range cmds {
				if strings.Contains(strings.ToLower(c.Name()), q) ||
					strings.Contains(strings.ToLower(c.Description()), q) {
					out = append(out, Capability{Kind: KindCommand, Name: c.Name(), Description: c.Description()})
				}
			}
		}
	}

	// Rank by kind (stable order: tool, skill, command), then name.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return kindRank(out[i].Kind) < kindRank(out[j].Kind)
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// kindRank returns the display/rank order for a Kind: tool before skill before
// command. The Kind string order ("command" < "skill" < "tool") is not the
// desired result order, so ranking is explicit.
func kindRank(k Kind) int {
	switch k {
	case KindTool:
		return 0
	case KindSkill:
		return 1
	case KindCommand:
		return 2
	default:
		return 3
	}
}
