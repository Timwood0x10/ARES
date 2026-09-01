package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// stubProvider is a minimal LLMProvider for registry tests.
type stubProvider struct{ name string }

func (s *stubProvider) Generate(context.Context, string) (string, error) { return "", nil }
func (s *stubProvider) IsEnabled() bool                                  { return true }
func (s *stubProvider) GetProvider() string                              { return s.name }

// stubFactory returns a Factory yielding a provider labelled name.
func stubFactory(name string) Factory {
	return func(map[string]any) (LLMProvider, error) { return &stubProvider{name: name}, nil }
}

// TestRegistryRegisterRejectsInvalidInput covers the two guard clauses. They
// matter because compat is the third-party plugin surface: a plugin that
// registers under an empty name, or hands over a nil factory, must fail at
// registration rather than at the first Lookup deep inside the runtime.
func TestRegistryRegisterRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		regName string
		factory Factory
	}{
		{name: "empty_name", regName: "", factory: stubFactory("x")},
		{name: "nil_factory", regName: "openai", factory: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			if err := r.Register(tt.regName, tt.factory); err == nil {
				t.Fatal("Register must reject invalid input")
			}
			if got := r.Names(); len(got) != 0 {
				t.Fatalf("a rejected registration must not be stored, got %v", got)
			}
		})
	}
}

// TestRegistryDuplicateRegistrationIsRejected pins first-writer-wins. Silently
// overwriting would let a late-loading plugin hijack the official "openai"
// adapter — the registry is process-wide via compat.Default.
func TestRegistryDuplicateRegistrationIsRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("openai", stubFactory("first")); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register("openai", stubFactory("second")); err == nil {
		t.Fatal("duplicate Register must fail")
	}

	f, err := r.Lookup("openai")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	p, err := f(nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if got := p.GetProvider(); got != "first" {
		t.Fatalf("the first registration must win, got %q", got)
	}
}

// TestRegistryLookupUnknownReturnsErrNotFound locks the sentinel so callers can
// distinguish "not installed" from "installed but broken" via errors.Is.
func TestRegistryLookupUnknownReturnsErrNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Lookup("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup(unknown) = %v, want ErrNotFound in the chain", err)
	}
}

// TestRegistryNamesListsRegistered checks the discovery surface used to report
// which providers an installation actually has.
func TestRegistryNamesListsRegistered(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"openai", "ollama"} {
		if err := r.Register(n, stubFactory(n)); err != nil {
			t.Fatalf("Register(%q): %v", n, err)
		}
	}
	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("Names() = %v, want 2 entries", names)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range []string{"openai", "ollama"} {
		if !seen[want] {
			t.Fatalf("Names() = %v, missing %q", names, want)
		}
	}
}

// TestRegistryConcurrentRegisterAndLookup exercises the RWMutex. The registry is
// a process-wide singleton (compat.Default) written during bootstrap and read
// from runtime components, so concurrent access is the normal case, not an edge
// case. Run with -race to be meaningful.
func TestRegistryConcurrentRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	const n = 32

	var wg sync.WaitGroup
	wg.Add(2 * n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			// Duplicate names are expected and their errors are the point of
			// the test (first-writer-wins under contention), so the result is
			// deliberately discarded.
			_ = r.Register("p", stubFactory("p"))
			_ = i
		}(i)
		go func() {
			defer wg.Done()
			_, _ = r.Lookup("p")
			_ = r.Names()
		}()
	}
	wg.Wait()

	if got := r.Names(); len(got) != 1 {
		t.Fatalf("concurrent duplicate registration must leave exactly one entry, got %v", got)
	}
}
