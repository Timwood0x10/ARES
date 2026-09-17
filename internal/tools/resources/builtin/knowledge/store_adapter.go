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
	obj, err := a.store.Get(ctx, tenantID, itemID)
	if err != nil {
		return nil, err
	}
	if obj == nil {
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
	// Fetch the existing row for field preservation only — NOT for ownership.
	// A tenant-scoped Get reports a foreign-namespace row as absent, so the
	// adapter cannot distinguish "no such row" from "another tenant's row", and
	// must not try: that would reopen the ID-enumeration hole the scoped Get
	// closed. Ownership is enforced in the store instead: Save refuses to
	// overwrite a row whose namespace differs (ErrObjectNotFound), which is
	// what stops a cross-tenant knowledge_update from migrating the victim row
	// into the caller's namespace via the upsert-on-miss path below.
	existing, gerr := a.store.Get(ctx, tenantID, item.ID)
	if gerr != nil {
		existing = nil
	}
	// Preserve the fields the round trip through KnowledgeItem cannot
	// carry (Raw, Representations, EmbeddingModel, Confidence, Version,
	// Type, Status, Quality, Relations). Overwriting the stored object
	// with a bare conversion previously dropped the embedding metadata, so
	// every knowledge_update silently degraded that object's semantic
	// recall to lexical-only.
	if existing != nil {
		if obj.Raw == nil {
			obj.Raw = existing.Raw
		}
		if len(existing.Representations) > 0 {
			obj.Representations = existing.Representations
		}
		if existing.EmbeddingModel != "" {
			obj.EmbeddingModel = existing.EmbeddingModel
		}
		if existing.Type != "" {
			obj.Type = existing.Type
		}
		if existing.Status != "" {
			obj.Status = existing.Status
		}
		if existing.Quality != nil {
			obj.Quality = existing.Quality
		}
		if len(existing.Relations) > 0 {
			obj.Relations = existing.Relations
		}
		if existing.Confidence != 0 {
			obj.Confidence = existing.Confidence
		}
		if existing.Version > obj.Version {
			obj.Version = existing.Version
		}
	}
	// Ownership on the write path is the store's Save guard (refuses to
	// overwrite a row owned by a different namespace), not anything checkable
	// here: a cross-tenant update reaches Save with the foreign row invisible
	// to the scoped Get above, and the guard is what turns that silent
	// migration into ErrObjectNotFound.
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
	obj, err := a.store.Get(ctx, tenantID, itemID)
	if err != nil {
		return err
	}
	// A nil object must be rejected, not treated as deletable: the pre-fix
	// check was `obj != nil && obj.Namespace != tenantID`, which skipped the
	// tenant test entirely when a store returned (nil, nil) and then deleted
	// by ID. The write path was therefore MORE permissive than the read path
	// (GetKnowledge already rejected nil). Any store backend that answers
	// (nil, nil) instead of an ErrObjectNotFound sentinel turned that
	// asymmetry into an unguarded delete.
	if obj == nil || obj.Namespace != tenantID {
		return errObjectNotFound
	}
	return a.store.Delete(ctx, tenantID, itemID)
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
