package knowledge

import (
	"context"
	"errors"
	"fmt"
)

// MigrateOptions tunes one namespace migration.
type MigrateOptions struct {
	// DryRun reports what would move without touching the store.
	DryRun bool
	// Limit caps how many objects one call moves (0 = every object). It exists
	// so a large corpus can be moved in bounded passes; because migration is a
	// move, a re-run simply resumes with what is left in the source.
	Limit int
}

// MigrateReport summarises one migration pass.
type MigrateReport struct {
	// Considered is how many objects the source namespace held.
	Considered int
	// Migrated is how many objects now live in the target namespace.
	Migrated int
	// Vectors is how many embedding vectors were re-attached to the moved
	// objects. It is reported because a gap here means the facts arrived
	// without their vectors — recall for them is lexical-only — and that must
	// be visible rather than silent.
	Vectors int
	// DryRun echoes the request mode.
	DryRun bool
}

// MigrateNamespace re-homes every object in one namespace into another.
//
// It is the primitive behind enabling user-scoped recall on a corpus written
// before any user was declared: those facts live under the tenant (or
// "default"), where user-scoped recall never looks.
//
// Migration is a MOVE, not a copy, for two reasons. The store keys objects by ID
// alone and refuses to re-home an ID under a different namespace (Save answers
// ErrObjectNotFound rather than let a caller re-home another scope's row), so a
// copy would require minting new IDs and rewriting every reference. And a copy
// left behind would keep the facts visible to exactly the tenant-scoped recall
// the move is meant to scope down.
//
// Because each object is deleted from the source and re-saved into the target:
//
//   - vectors are captured BEFORE the first delete and re-attached afterwards,
//     since both backends destroy a representation together with its owning
//     object (the SQL schema cascades the delete);
//   - a failed save is rolled back into the source namespace instead of
//     dropping the fact, and the pass stops there so the caller can re-run it
//     once the cause is fixed. Already-moved objects are no longer in the
//     source, which is what makes a re-run resume rather than repeat.
func MigrateNamespace(ctx context.Context, store KnowledgeStore, from, to string, opts MigrateOptions) (MigrateReport, error) {
	report := MigrateReport{DryRun: opts.DryRun}
	if from == "" || to == "" {
		return report, errors.New("migrate namespace: source and target namespaces are both required")
	}
	if from == to {
		return report, errors.New("migrate namespace: source and target are the same namespace")
	}

	objects, err := store.Query(ctx, Query{Namespace: from, Limit: 0})
	if err != nil {
		return report, fmt.Errorf("migrate namespace: query %q: %w", from, err)
	}
	report.Considered = len(objects)
	if opts.Limit > 0 && len(objects) > opts.Limit {
		objects = objects[:opts.Limit]
	}
	if opts.DryRun || len(objects) == 0 {
		report.Migrated = len(objects)
		return report, nil
	}

	vectors, err := vectorsByObject(ctx, store, from)
	if err != nil {
		return report, err
	}

	moved := make([]*KnowledgeObject, 0, len(objects))
	for _, obj := range objects {
		if err := moveObject(ctx, store, obj, from, to, vectors); err != nil {
			// Attach the vectors of what did move before reporting, so a partial
			// pass is not also a lossy one.
			report.Migrated = len(moved)
			report.Vectors, _ = attachVectors(ctx, store, moved, vectors)
			return report, err
		}
		moved = append(moved, obj)
	}

	report.Migrated = len(moved)
	attached, attachErr := attachVectors(ctx, store, moved, vectors)
	report.Vectors = attached
	if attachErr != nil {
		// The facts moved but some vectors did not: say so with the counts, so
		// the caller can report exactly how many objects recall lexically now.
		return report, fmt.Errorf(
			"migrate namespace: %d object(s) moved into %q but re-attaching vectors failed "+
				"(those objects now recall lexically): %w", report.Migrated, to, attachErr)
	}
	return report, nil
}

// vectorsByObject returns the representations owned by objects in namespace,
// grouped by object id. A store that cannot enumerate representations yields an
// empty map: the facts still migrate, they simply arrive without their vectors —
// which the report's Vectors count makes visible.
func vectorsByObject(ctx context.Context, store KnowledgeStore, namespace string) (map[string][]*Representation, error) {
	reps, err := ListRepresentations(ctx, store, namespace)
	if err != nil {
		if errors.Is(err, ErrRepresentationListingNotSupported) {
			// An empty map, not nil: "this store cannot enumerate vectors" is a
			// real state, not the absence of an answer, and callers should not
			// have to distinguish the two.
			return map[string][]*Representation{}, nil
		}
		return nil, fmt.Errorf("migrate namespace: list representations: %w", err)
	}
	byObject := make(map[string][]*Representation, len(reps))
	for _, rep := range reps {
		if rep == nil {
			continue
		}
		byObject[rep.ObjectID] = append(byObject[rep.ObjectID], rep)
	}
	return byObject, nil
}

// moveObject deletes obj from `from` and saves it into `to`, rolling the delete
// back when the save fails so a failed move never drops the fact.
func moveObject(
	ctx context.Context,
	store KnowledgeStore,
	obj *KnowledgeObject,
	from, to string,
	vectors map[string][]*Representation,
) error {
	source := obj.Namespace
	if err := store.Delete(ctx, from, obj.ID); err != nil {
		return fmt.Errorf("migrate namespace: delete %q from %q: %w", obj.ID, from, err)
	}

	obj.Namespace = to
	if err := store.Save(ctx, obj); err != nil {
		obj.Namespace = source
		if rbErr := store.Save(ctx, obj); rbErr != nil {
			return fmt.Errorf(
				"migrate namespace: object %q: save into %q failed (%v) and the rollback into %q also failed (%v): "+
					"the object now survives only in a pre-migration snapshot", obj.ID, to, err, source, rbErr)
		}
		// The delete cascaded this object's vectors away; put them back so the
		// rolled-back object is whole again.
		for _, rep := range vectors[obj.ID] {
			if rErr := store.SaveRepresentation(ctx, rep); rErr != nil {
				return fmt.Errorf(
					"migrate namespace: object %q rolled back into %q but its vector %q was lost: %w",
					obj.ID, source, rep.ID, rErr)
			}
		}
		return fmt.Errorf("migrate namespace: object %q: save into %q: %w", obj.ID, to, err)
	}
	return nil
}

// attachVectors re-saves the vectors of objects that just moved. It runs AFTER
// the objects because a representation references its owning object (the
// Postgres foreign key enforces it).
func attachVectors(
	ctx context.Context,
	store KnowledgeStore,
	moved []*KnowledgeObject,
	vectors map[string][]*Representation,
) (int, error) {
	attached := 0
	for _, obj := range moved {
		for _, rep := range vectors[obj.ID] {
			if err := store.SaveRepresentation(ctx, rep); err != nil {
				return attached, fmt.Errorf("save vector %q for object %q: %w", rep.ID, obj.ID, err)
			}
			attached++
		}
	}
	return attached, nil
}
