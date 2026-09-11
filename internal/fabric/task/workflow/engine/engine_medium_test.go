package engine

// §3.2 (MEDIUM) regressions for the workflow engine:
// reloader deletion handling, ResetFromSteps graph events, SchedulerType
// access serialization, OutputStore Close safety, and definition field
// extraction anchoring.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReloadLoader is a WorkflowLoader that derives workflows from the file
// name — enough for scanAndLoad tests, which only need Load to succeed for
// existing files.
type fakeReloadLoader struct{}

func (fakeReloadLoader) Load(_ context.Context, source string) (*Workflow, error) {
	base := filepath.Base(source)
	ext := filepath.Ext(base)
	return &Workflow{
		ID:        base[:len(base)-len(ext)],
		Name:      base,
		UpdatedAt: time.Now().Add(-time.Hour), // older than file mtime → first scan installs
	}, nil
}

// TestScanAndLoadRemovesDeletedWorkflow pins the deletion contract: the
// watched directory is the source of truth — a workflow whose file is gone
// must leave the loaded map on the next scan. Pre-fix, the "modified" check
// only inspected loaded entries, so a deletion alone never triggered the
// map replace and deleted workflows stayed registered forever.
func TestScanAndLoadRemovesDeletedWorkflow(t *testing.T) {
	dir := t.TempDir()
	writeWorkflowFile(t, dir, "alpha.json")
	writeWorkflowFile(t, dir, "beta.json")

	w, err := NewFileWatcher(fakeReloadLoader{}, map[string]*Workflow{})
	require.NoError(t, err)
	defer w.Close()

	require.NoError(t, w.scanAndLoad(context.Background(), dir))
	w.mu.RLock()
	_, hasAlpha := w.workflows["alpha"]
	_, hasBeta := w.workflows["beta"]
	w.mu.RUnlock()
	require.True(t, hasAlpha, "alpha must load")
	require.True(t, hasBeta, "beta must load")

	// Delete one file and rescan: the deleted workflow must disappear even
	// though every REMAINING file is unchanged.
	require.NoError(t, os.Remove(filepath.Join(dir, "beta.json")))
	require.NoError(t, w.scanAndLoad(context.Background(), dir))

	w.mu.RLock()
	defer w.mu.RUnlock()
	_, hasAlpha2 := w.workflows["alpha"]
	_, hasBeta2 := w.workflows["beta"]
	assert.True(t, hasAlpha2, "remaining workflow must stay")
	assert.False(t, hasBeta2, "deleted workflow must be removed from the map")
}

// writeWorkflowFile writes a minimal workflow file (contents are irrelevant —
// the fake loader derives the workflow from the file name).
func writeWorkflowFile(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(`{"id":"x"}`), 0o644))
}

// TestResetFromStepsPublishesGraphEvent pins the graph-event contract of the
// rollback primitive: ResetFromSteps rebuilds the whole topology in place,
// and subscribers (the incremental compiler) must be notified — a silent
// reset left projected fabric tasks stale relative to the restored graph.
// (The fix predates this batch; this test locks it in.)
func TestResetFromStepsPublishesGraphEvent(t *testing.T) {
	dag, err := NewMutableDAG([]*Step{{ID: "a"}, {ID: "b"}})
	require.NoError(t, err)

	subID, ch := dag.SubscribeWithID()
	defer dag.Unsubscribe(subID)

	require.NoError(t, dag.ResetFromSteps([]*Step{{ID: "x"}, {ID: "y"}}))

	select {
	case ev := <-ch:
		assert.Equal(t, ChangeReset, ev.Change.Type)
		assert.True(t, ev.Success)
	case <-time.After(2 * time.Second):
		t.Fatal("ResetFromSteps must publish a ChangeReset graph event")
	}
}

// TestSchedulerTypeConcurrentSetAndRead pins the serialization of the
// scheduler-type override: SetSchedulerType (genome evolution patch) must
// not race GetExecutionOrder's read. Under -race, the previous public
// unlocked field failed this test.
func TestSchedulerTypeConcurrentSetAndRead(t *testing.T) {
	dag, err := NewMutableDAG([]*Step{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	require.NoError(t, err)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		flip := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			flip = !flip
			if flip {
				dag.SetSchedulerType("*graph.RandomScheduler")
			} else {
				dag.SetSchedulerType("")
			}
		}
	}()

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = dag.GetExecutionOrder()
				_ = dag.SchedulerTypeOf()
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestOutputStoreCloseThenUseIsSafe pins the Close contract: after Close,
// Set is a no-op and Get reports absence — never a panic on the nil backing
// map. (The fix predates this batch; this test locks it in.)
func TestOutputStoreCloseThenUseIsSafe(t *testing.T) {
	store := NewOutputStore()
	store.Set("s1", &StepOutput{StepID: "s1", Output: "before"})
	require.NotNil(t, func() *StepOutput {
		out, _ := store.Get("s1")
		return out
	}())

	store.Close()

	assert.NotPanics(t, func() {
		store.Set("s2", &StepOutput{StepID: "s2", Output: "after"})
		store.Get("s1")
		store.GetMultiple([]string{"s1", "s2"})
	})

	if _, exists := store.Get("s2"); exists {
		t.Fatal("Set after Close must be a no-op")
	}
}

// TestExtractFieldAnchoredToLineStart pins the field-extraction anchoring:
// `name` must not match inside `username: alice`. Pre-fix the unanchored
// regex extracted "alice" for the name field of a definition that also
// carried a username line, silently binding the agent to the wrong identity.
func TestExtractFieldAnchoredToLineStart(t *testing.T) {
	p := NewDefinitionParser()

	content := "username: alice\nname: bob\ntype: coder\n"
	got, err := p.extractField(content, "name")
	require.NoError(t, err)
	assert.Equal(t, "bob", got, "name must match the `name:` line, not the substring inside `username:`")

	got, err = p.extractField(content, "type")
	require.NoError(t, err)
	assert.Equal(t, "coder", got)

	// A field that only appears as a suffix of another identifier is NOT
	// found.
	_, err = p.extractField("username: alice\n", "name")
	assert.ErrorIs(t, err, ErrFieldNotFound)

	// Indented / bulleted definitions still extract.
	got, err = p.extractField("  - name: carol\n", "name")
	require.NoError(t, err)
	assert.Equal(t, "carol", got)
}
