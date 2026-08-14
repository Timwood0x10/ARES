// Package skills implements progressive disclosure for agent capabilities
// (ares-vs-prime-agent 5.7, high priority): only a skill's name and one-line
// description are resident in the LLM context; the full detail body is loaded
// on demand by ID. This mirrors prime-agent's SkillFrontmatter model —
// description always present, SKILL.md fetched only when the agent asks.
package skills

import (
	"fmt"
	"sort"
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

// LoadDetail returns the full skill body for name. ok is false when the skill
// is unknown — the caller renders "unknown skill" instead of a broken body.
func (r *Registry) LoadDetail(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	if !ok {
		return "", false
	}
	return s.Detail, true
}

// Has reports whether a skill is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.skills[name]
	return ok
}

// Count returns the number of registered skills.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}
