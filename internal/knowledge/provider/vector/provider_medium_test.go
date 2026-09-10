package vector

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/storage"
)

// fakeVectorStore is a minimal storage.VectorStore whose CreateCollection
// result is scripted by the test.
type fakeVectorStore struct {
	createErr error
}

func (f *fakeVectorStore) Search(_ context.Context, _, _ string, _ []float64, _ int) ([]*storage.SearchResult, error) {
	return nil, nil
}

func (f *fakeVectorStore) AddEmbedding(_ context.Context, _, _ string, _ []float64, _ map[string]any) error {
	return nil
}

func (f *fakeVectorStore) CreateCollection(_ context.Context, _ string, _ int) error {
	return f.createErr
}

// TestNewVectorProviderCreateCollectionErrorPropagates locks REVIEW 3.5:
// NewVectorProvider used to discard the CreateCollection error entirely, so
// an invalid collection name or a database outage was only discovered later
// as an opaque search failure. Real errors must fail construction; a nil
// error (collection exists — CREATE TABLE IF NOT EXISTS) succeeds.
func TestNewVectorProviderCreateCollectionErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom: relation cannot be created")

	_, err := NewVectorProvider(&fakeVectorStore{createErr: wantErr}, Config{
		Name:            "test-provider",
		Collection:      "test_collection",
		TenantID:        "default",
		VectorDimension: 8,
	})
	if err == nil {
		t.Fatal("NewVectorProvider must fail when CreateCollection fails")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error must wrap the CreateCollection error, got: %v", err)
	}

	// Happy path: collection creation succeeds (or already exists).
	p, err := NewVectorProvider(&fakeVectorStore{}, Config{
		Name:            "test-provider",
		Collection:      "test_collection",
		TenantID:        "default",
		VectorDimension: 8,
	})
	if err != nil {
		t.Fatalf("NewVectorProvider with successful CreateCollection: %v", err)
	}
	if p == nil || p.Name() != "test-provider" {
		t.Fatalf("provider not constructed correctly: %+v", p)
	}
}
