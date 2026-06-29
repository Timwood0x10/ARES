// custom-store demonstrates implementing a custom ServiceStore
// for persistent service discovery (SQLite, Postgres, etc.).
//
// Run: go run ./examples/custom-store
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Timwood0x10/ares/api/discovery"
)

func main() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "discovery-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	// Use JSON file store instead of memory.
	store := NewJSONFileStore(filepath.Join(dir, "services.json"))

	engine := discovery.NewEngine(discovery.EngineConfig{
		Store: store,
	})

	// Register a service.
	_ = engine.Register(ctx, discovery.RegisterRequest{
		Name:     "my-mcp",
		Endpoint: "/usr/bin/my-mcp",
		Tags:     []string{"capability:search"},
		Metadata: map[string]string{"version": "1.0"},
	})

	// Verify persisted to file.
	data, _ := os.ReadFile(filepath.Join(dir, "services.json"))
	fmt.Println("=== Persisted to file ===")
	fmt.Println(string(data))

	// Create new engine with same store — data survives restart.
	engine2 := discovery.NewEngine(discovery.EngineConfig{
		Store: store,
	})
	services, _ := engine2.List(ctx)
	fmt.Printf("\n=== After 'restart': %d services ===\n", len(services))
	for _, svc := range services {
		fmt.Printf("  %s tags=%v\n", svc.Identity.Name, svc.Identity.Tags)
	}
}

// JSONFileStore is a file-backed ServiceStore for demonstration.
type JSONFileStore struct {
	path string
}

func NewJSONFileStore(path string) *JSONFileStore {
	return &JSONFileStore{path: path}
}

func (s *JSONFileStore) Save(_ context.Context, svc *discovery.DiscoveredService) error {
	services := s.load()
	// Update or insert.
	found := false
	for i, existing := range services {
		if existing.Identity.ID == svc.Identity.ID {
			services[i] = svc
			found = true
			break
		}
	}
	if !found {
		services = append(services, svc)
	}
	return s.save(services)
}

func (s *JSONFileStore) Get(_ context.Context, id string) (*discovery.DiscoveredService, error) {
	for _, svc := range s.load() {
		if svc.Identity.ID == id {
			return svc, nil
		}
	}
	return nil, nil
}

func (s *JSONFileStore) List(_ context.Context) ([]*discovery.DiscoveredService, error) {
	return s.load(), nil
}

func (s *JSONFileStore) Delete(_ context.Context, id string) error {
	services := s.load()
	filtered := make([]*discovery.DiscoveredService, 0, len(services))
	for _, svc := range services {
		if svc.Identity.ID != id {
			filtered = append(filtered, svc)
		}
	}
	return s.save(filtered)
}

func (s *JSONFileStore) load() []*discovery.DiscoveredService {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var services []*discovery.DiscoveredService
	_ = json.Unmarshal(data, &services)
	return services
}

func (s *JSONFileStore) save(services []*discovery.DiscoveredService) error {
	data, err := json.MarshalIndent(services, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}
