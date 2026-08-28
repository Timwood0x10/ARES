package builtin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// errObjectNotFound is returned when a tenant-scoped read/delete finds no
// object with the given ID (the knowledge package documents ErrObjectNotFound
// in the store contract but does not export a sentinel value).
var errObjectNotFound = errors.New("knowledge object not found")

// StoreAdapter adapts a knowledge.KnowledgeStore to the builtin
// KnowledgeSearcher and KnowledgeService interfaces. It is the wiring point
// that makes the knowledge_search / knowledge_add / knowledge_update /
// knowledge_delete tools actually usable (not just nil-guarded) when a store
// is available from bootstrap.
//
// Tenant isolation: the store's Namespace field carries the tenantID, so a
// search or read scoped to one tenant only touches that tenant's objects.
type StoreAdapter struct {
	store knowledge.KnowledgeStore
}

// NewStoreAdapter creates a StoreAdapter over the given store.
func NewStoreAdapter(store knowledge.KnowledgeStore) *StoreAdapter {
	return &StoreAdapter{store: store}
}

// Search implements KnowledgeSearcher via HybridSearch (lexical fallback when
// no embedding is wired). Results are ranked by FinalScore descending.
func (a *StoreAdapter) Search(ctx context.Context, tenantID, query string) ([]*RetrievalResult, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("knowledge store adapter: store is nil")
	}
	scored, err := a.store.HybridSearch(ctx, knowledge.HybridSearchRequest{
		Query:     query,
		Namespace: tenantID,
		FinalK:    10,
	})
	if err != nil {
		return nil, fmt.Errorf("store search: %w", err)
	}
	results := make([]*RetrievalResult, 0, len(scored))
	for _, so := range scored {
		if so.Object == nil {
			continue
		}
		results = append(results, &RetrievalResult{
			ID:       so.Object.ID,
			Score:    so.FinalScore,
			Content:  objectText(so.Object),
			Source:   objectSource(so.Object),
			Metadata: so.Object.Metadata,
		})
	}
	return results, nil
}

// GetKnowledge implements KnowledgeService.GetKnowledge.
func (a *StoreAdapter) GetKnowledge(ctx context.Context, tenantID, itemID string) (*KnowledgeItem, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("knowledge store adapter: store is nil")
	}
	obj, err := a.store.Get(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if obj == nil || obj.Namespace != tenantID {
		return nil, errObjectNotFound
	}
	return toKnowledgeItem(obj), nil
}

// UpdateKnowledge implements KnowledgeService.UpdateKnowledge.
func (a *StoreAdapter) UpdateKnowledge(ctx context.Context, tenantID string, item *KnowledgeItem) (*KnowledgeItem, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("knowledge store adapter: store is nil")
	}
	if item == nil {
		return nil, errors.New("knowledge item is nil")
	}
	obj := fromKnowledgeItem(item)
	obj.Namespace = tenantID
	if err := a.store.Save(ctx, obj); err != nil {
		return nil, err
	}
	return toKnowledgeItem(obj), nil
}

// AddKnowledge implements KnowledgeService.AddKnowledge.
func (a *StoreAdapter) AddKnowledge(ctx context.Context, item *KnowledgeItem) (*KnowledgeItem, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("knowledge store adapter: store is nil")
	}
	if item == nil {
		return nil, errors.New("knowledge item is nil")
	}
	obj := fromKnowledgeItem(item)
	if err := a.store.Save(ctx, obj); err != nil {
		return nil, err
	}
	return toKnowledgeItem(obj), nil
}

// DeleteKnowledge implements KnowledgeService.DeleteKnowledge.
func (a *StoreAdapter) DeleteKnowledge(ctx context.Context, tenantID, itemID string) error {
	if a == nil || a.store == nil {
		return errors.New("knowledge store adapter: store is nil")
	}
	obj, err := a.store.Get(ctx, itemID)
	if err != nil {
		return err
	}
	if obj != nil && obj.Namespace != tenantID {
		return errObjectNotFound
	}
	return a.store.Delete(ctx, itemID)
}

// objectText returns the most complete text representation of an object.
func objectText(obj *knowledge.KnowledgeObject) string {
	if obj.Normalized != "" {
		return obj.Normalized
	}
	if obj.Summary != "" {
		return obj.Summary
	}
	return string(obj.Raw)
}

// objectSource extracts a human-readable source from object metadata.
func objectSource(obj *knowledge.KnowledgeObject) string {
	if v, ok := obj.Metadata["source"].(string); ok {
		return v
	}
	return ""
}

// toKnowledgeItem converts a KnowledgeObject to the builtin KnowledgeItem.
func toKnowledgeItem(obj *knowledge.KnowledgeObject) *KnowledgeItem {
	return &KnowledgeItem{
		ID:        obj.ID,
		TenantID:  obj.Namespace,
		Content:   objectText(obj),
		Source:    objectSource(obj),
		Tags:      obj.Tags,
		CreatedAt: obj.CreatedAt,
		UpdatedAt: obj.UpdatedAt,
		Metadata:  obj.Metadata,
	}
}

// fromKnowledgeItem converts a builtin KnowledgeItem to a KnowledgeObject.
func fromKnowledgeItem(item *KnowledgeItem) *knowledge.KnowledgeObject {
	now := time.Now()
	obj := &knowledge.KnowledgeObject{
		ID:         item.ID,
		Namespace:  item.TenantID,
		Normalized: item.Content,
		Summary:    item.Content,
		Tags:       item.Tags,
		Metadata:   item.Metadata,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
	}
	if obj.Metadata == nil {
		obj.Metadata = map[string]any{}
	}
	if item.Source != "" {
		obj.Metadata["source"] = item.Source
	}
	if item.CreatedAt.IsZero() {
		obj.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		obj.UpdatedAt = now
	}
	return obj
}
