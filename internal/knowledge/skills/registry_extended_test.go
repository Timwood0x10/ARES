package skills

import (
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.Count() != 0 {
		t.Errorf("Count() = %d, want 0", r.Count())
	}
}

func TestRegistryRegister(t *testing.T) {
	r := NewRegistry()
	tests := []struct {
		name    string
		skill   Skill
		wantErr bool
	}{
		{"valid", Skill{Name: "shell", Description: "run shell commands"}, false},
		{"empty_name", Skill{Description: "desc"}, true},
		{"replace_existing", Skill{Name: "shell", Description: "updated"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.Register(tt.skill)
			if (err != nil) != tt.wantErr {
				t.Errorf("Register(%+v) err = %v, wantErr %v", tt.skill, err, tt.wantErr)
			}
		})
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Skill{Name: "zebra", Description: "z desc"})
	_ = r.Register(Skill{Name: "apple", Description: "a desc"})
	_ = r.Register(Skill{Name: "mango", Description: "m desc", Detail: "full body"})

	list := r.List()
	if len(list) != 3 {
		t.Fatalf("List() len = %d, want 3", len(list))
	}
	// Should be sorted by name.
	if list[0].Name != "apple" || list[1].Name != "mango" || list[2].Name != "zebra" {
		t.Errorf("List() order = [%s, %s, %s], want [apple, mango, zebra]",
			list[0].Name, list[1].Name, list[2].Name)
	}
	// Detail should be stripped from List results.
	for _, s := range list {
		if s.Detail != "" {
			t.Errorf("List() returned Detail for %q, want empty", s.Name)
		}
	}
}

func TestRegistryHas(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Skill{Name: "existing", Description: "desc"})

	if !r.Has("existing") {
		t.Error("Has(existing) = false, want true")
	}
	if r.Has("nonexistent") {
		t.Error("Has(nonexistent) = true, want false")
	}
}

func TestRegistryCount(t *testing.T) {
	r := NewRegistry()
	if r.Count() != 0 {
		t.Errorf("Count() = %d, want 0", r.Count())
	}
	_ = r.Register(Skill{Name: "a"})
	_ = r.Register(Skill{Name: "b"})
	if r.Count() != 2 {
		t.Errorf("Count() = %d, want 2", r.Count())
	}
}

func TestRegistryLoadDetail(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Skill{Name: "with_detail", Description: "desc", Detail: "full body"})
	_ = r.Register(Skill{Name: "no_detail", Description: "desc"})

	tests := []struct {
		name     string
		skill    string
		wantBody string
		wantOK   bool
	}{
		{"with_detail", "with_detail", "full body", true},
		{"no_detail_no_loader", "no_detail", "", true},
		{"nonexistent", "missing", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, ok := r.LoadDetail(tt.skill)
			if ok != tt.wantOK {
				t.Errorf("LoadDetail(%q) ok = %v, want %v", tt.skill, ok, tt.wantOK)
			}
			if body != tt.wantBody {
				t.Errorf("LoadDetail(%q) body = %q, want %q", tt.skill, body, tt.wantBody)
			}
		})
	}
}

func TestRegistryLoadDetailWithLoader(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Skill{Name: "lazy", Description: "desc"})
	r.SetDetailLoader(func(name string) (string, bool) {
		if name == "lazy" {
			return "loaded on demand", true
		}
		if name == "external" {
			return "external skill", true
		}
		return "", false
	})

	// Registered skill with empty Detail → loader resolves it.
	body, ok := r.LoadDetail("lazy")
	if !ok || body != "loaded on demand" {
		t.Errorf("LoadDetail(lazy) = (%q, %v), want (loaded on demand, true)", body, ok)
	}

	// Unregistered skill that loader knows → loader resolves it.
	body, ok = r.LoadDetail("external")
	if !ok || body != "external skill" {
		t.Errorf("LoadDetail(external) = (%q, %v), want (external skill, true)", body, ok)
	}

	// Unknown skill, loader doesn't know → not found.
	_, ok = r.LoadDetail("unknown")
	if ok {
		t.Error("LoadDetail(unknown) ok = true, want false")
	}
}

func TestRegistrySetDetailLoaderNil(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Skill{Name: "lazy", Description: "desc"})
	r.SetDetailLoader(nil) // detach
	body, ok := r.LoadDetail("lazy")
	if !ok || body != "" {
		t.Errorf("LoadDetail(lazy) after nil loader = (%q, %v), want ('', true)", body, ok)
	}
}

func TestRegistrySearch(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Skill{Name: "web-search", Description: "search the web"})
	_ = r.Register(Skill{Name: "shell", Description: "run shell commands"})
	_ = r.Register(Skill{Name: "file-ops", Description: "file search and operations"})
	_ = r.Register(Skill{Name: "math", Description: "calculate expressions"})

	tests := []struct {
		name      string
		query     string
		limit     int
		wantCount int
		wantFirst string
	}{
		{"name_match", "search", 0, 2, "web-search"}, // name matches sorted: file-ops(desc), web-search(name) — name first
		{"desc_match", "shell", 0, 1, "shell"},
		{"case_insensitive", "SHELL", 0, 1, "shell"},
		{"limit", "e", 2, 2, ""},
		{"empty_query", "", 0, 0, ""},
		{"no_match", "xyz", 0, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := r.Search(tt.query, tt.limit)
			if len(results) != tt.wantCount {
				t.Errorf("Search(%q, %d) len = %d, want %d", tt.query, tt.limit, len(results), tt.wantCount)
			}
			if tt.wantFirst != "" && len(results) > 0 && results[0].Name != tt.wantFirst {
				t.Errorf("Search(%q) first = %q, want %q", tt.query, results[0].Name, tt.wantFirst)
			}
			// Detail should be stripped from Search results.
			for _, s := range results {
				if s.Detail != "" {
					t.Errorf("Search returned Detail for %q", s.Name)
				}
			}
		})
	}
}

func TestRegistrySearchNameBeforeDesc(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Skill{Name: "alpha", Description: "beta tool"})
	_ = r.Register(Skill{Name: "beta", Description: "alpha tool"})

	results := r.Search("alpha", 0)
	if len(results) < 2 {
		t.Fatalf("Search(alpha) len = %d, want >= 2", len(results))
	}
	// Name match ("alpha") should come before desc match ("beta" whose desc has "alpha").
	if results[0].Name != "alpha" {
		t.Errorf("first result = %q, want alpha (name match priority)", results[0].Name)
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = r.Register(Skill{Name: "skill", Description: "desc"})
			_ = r.List()
			r.Has("skill")
			r.Count()
			r.Search("skill", 10)
			r.LoadDetail("skill")
		}
	}()
	<-done
	// After concurrent writes, the registry must contain exactly one skill.
	if r.Count() != 1 {
		t.Errorf("Count() after concurrent access = %d, want 1", r.Count())
	}
	if !r.Has("skill") {
		t.Error("Has(skill) = false after concurrent access")
	}
}
