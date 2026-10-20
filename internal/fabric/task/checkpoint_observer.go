package taskfabric

// CheckpointDecodeObserver receives the outcome of a checkpoint read-back.
// The observer is scoped to one Fabric: DecodeCheckpoint is a pure package
// function, so only the Fabric method counts a decode as a data-consistency
// sample. Keeping the observer on the Fabric (rather than a package global)
// means an evaluation/scratch fabric cannot pollute the serving fabric's
// metric, and tests running in the same process cannot observe each other.
type CheckpointDecodeObserver interface {
	// OnCheckpointDecode reports whether the checkpoint decoded successfully.
	// Implementations must be cheap and must not panic (the fabric recovers
	// panics defensively, but a panicking observer is still a bug).
	OnCheckpointDecode(ok bool)
}

// checkpointDecodeObserverSlot wraps the interface so it can live in an
// atomic.Pointer without allocating on every read.
type checkpointDecodeObserverSlot struct {
	observer CheckpointDecodeObserver
}

// WithConsistencyObserver installs the checkpoint read-back observer. It is
// the data-consistency half of the observability wiring, shaped like the
// recovery observer: the fabric calls it once per Fabric.DecodeCheckpoint,
// and the arena side adapts those outcomes to MetricsCollector.
//
// Nil clears the observer. The method is safe to call while tasks run, but
// production wires it once during assembly.
func (f *Fabric) WithConsistencyObserver(obs CheckpointDecodeObserver) *Fabric {
	if f == nil {
		return f
	}
	if obs == nil {
		f.decodeObserver.Store(nil)
		return f
	}
	f.decodeObserver.Store(&checkpointDecodeObserverSlot{observer: obs})
	return f
}

// DecodeCheckpoint is the observed form of the package decode function. Every
// task-lifecycle read-back must use this method (rather than the package
// function) so a failed decode is counted; DecodeCheckpoint itself stays pure
// for callers that have no Fabric (tests, pure helpers).
func (f *Fabric) DecodeCheckpoint(cp any) (DecodedCheckpoint, error) {
	dc, err := DecodeCheckpoint(cp)
	f.notifyCheckpointDecode(err == nil)
	return dc, err
}

// notifyCheckpointDecode delivers one sample. A panicking observer must never
// break a task transition, so the notification is panic-safe (the same rule as
// the recovery observer).
func (f *Fabric) notifyCheckpointDecode(ok bool) {
	if f == nil {
		return
	}
	slot := f.decodeObserver.Load()
	if slot == nil || slot.observer == nil {
		return
	}
	defer func() { _ = recover() }()
	slot.observer.OnCheckpointDecode(ok)
}
