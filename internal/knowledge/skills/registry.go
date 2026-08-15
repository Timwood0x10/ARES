// Package skills implements progressive disclosure for agent capabilities
// (ares-vs-prime-agent 5.7, high priority): only a skill's name and one-line
// description are resident in the LLM context; the full detail body is loaded
// on demand by ID. This mirrors prime-agent's SkillFrontmatter model —
// description always present, SKILL.md fetched only when the agent asks.
package skills

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Skill is one discoverable capability. Description is the always-resident
// one-liner; Detail is the full body fetched on demand. Keeping Detail out of
// the registry list saves tokens when only the description is needed.
type Skill struct {
	// Name is the stable identifier (e.g. "shell", "web-search").
	Name string
	// Description is the one-line summary always resident in context.
	Description string
	// Detail is the full skill body, loaded on demand.
	Detail string
}

// Registry holds skills and exposes progressive disclosure: List returns only
// name+description; LoadDetail returns the full body for one skill. It is safe
// for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	skills map[string]Skill
	// detailLoader, when set, fetches a skill body on demand whenever a
	// registered skill has no in-memory Detail (e.g. the skill catalog backs
	// the registry without loading every SKILL.md at seed time). The loader
	// keeps the knowledge/skills package free of a dependency on the catalog.
	// Guarded by mu.
	detailLoader func(name string) (string, bool)
}

// NewRegistry creates an empty skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]Skill),
	}
}

// Register adds or replaces a skill. Name must be non-empty.
func (r *Registry) Register(s Skill) error {
	if s.Name == "" {
		return fmt.Errorf("skills: name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Name] = s
	return nil
}

// List returns the always-resident view: name + description only, sorted by
// name for deterministic prompt rendering. Detail is intentionally omitted.
func (r *Registry) List() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, Skill{Name: s.Name, Description: s.Description})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SetDetailLoader attaches an on-demand detail loader. When set, LoadDetail
// falls back to it for registered skills whose in-memory Detail is empty (and
// for names the loader knows but the registry does not). nil detaches it.
//
// Args:
//   - fn: the loader (name -> body, found), or nil.
func (r *Registry) SetDetailLoader(fn func(name string) (string, bool)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detailLoader = fn
}

// LoadDetail returns the full skill body for name. ok is false when the skill
// is unknown (and no detail loader can supply it) — the caller renders
// "unknown skill" instead of a broken body. A registered skill with an empty
// in-memory Detail is resolved lazily through the detail loader when one is
// attached, so the body is fetched on demand without holding the registry lock.
func (r *Registry) LoadDetail(name string) (string, bool) {
	r.mu.RLock()
	s, ok := r.skills[name]
	loader := r.detailLoader
	r.mu.RUnlock()

	if !ok {
		if loader != nil {
			return loader(name)
		}
		return "", false
	}
	if s.Detail != "" {
		return s.Detail, true
	}
	if loader != nil {
		return loader(name)
	}
	return "", true
}

// Has reports whether a skill is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.skills[name]
	return ok
}

// Search returns skills matching query by case-insensitive substring on name
// or description, mirroring cc-switch's skill search (filter by name and
// description). Results are ranked: name matches first, then description
// matches; each group is sorted by name. Limit <= 0 returns all matches.
func (r *Registry) Search(query string, limit int) []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}

	var nameHits, descHits []Skill
	for _, s := range r.skills {
		if strings.Contains(strings.ToLower(s.Name), q) {
			nameHits = append(nameHits, s)
			continue
		}
		if strings.Contains(strings.ToLower(s.Description), q) {
			descHits = append(descHits, s)
		}
	}
	sort.Slice(nameHits, func(i, j int) bool { return nameHits[i].Name < nameHits[j].Name })
	sort.Slice(descHits, func(i, j int) bool { return descHits[i].Name < descHits[j].Name })

	merged := make([]Skill, 0, len(nameHits)+len(descHits))
	merged = append(merged, nameHits...)
	merged = append(merged, descHits...)
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// Count returns the number of registered skills.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}
